package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/serve/proxy"
)

func newProxyTest(t *testing.T) (*proxyServer, *proxy.Store) {
	t.Helper()
	st, err := proxy.Open(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatalf("proxy.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{
		"anthropic": {Models: []string{"claude-x"}},
	}}
	p := &proxyServer{
		store:       st,
		catalog:     proxy.BuildCatalog(cfg),
		adminToken:  "admin-secret",
		requireAuth: true,
	}
	return p, st
}

func TestEnsureProxyExclusive(t *testing.T) {
	if err := ensureProxyExclusive([]string{"web"}); err != nil {
		t.Fatalf("web alone should be fine: %v", err)
	}
	if err := ensureProxyExclusive([]string{"proxy"}); err != nil {
		t.Fatalf("proxy alone should be fine: %v", err)
	}
	if err := ensureProxyExclusive([]string{"proxy", "web"}); err == nil {
		t.Fatal("proxy combined with web should error")
	}
	if err := ensureProxyExclusive([]string{"web", "proxy", "jobs"}); err == nil {
		t.Fatal("proxy combined with others should error")
	}
}

func TestPeekAndRewriteRequestModel(t *testing.T) {
	body := []byte(`{"model":"a/b","messages":[{"role":"user"}],"temperature":0.5}`)
	if got := peekRequestModel(body); got != "a/b" {
		t.Fatalf("peek = %q", got)
	}
	// Rewrite to a concrete model preserves other fields.
	out, err := rewriteRequestModel(body, "concrete")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if peekRequestModel(out) != "concrete" {
		t.Fatalf("rewritten model = %q", peekRequestModel(out))
	}
	if !strings.Contains(string(out), `"temperature":0.5`) {
		t.Fatalf("other fields lost: %s", out)
	}
	// Empty model removes the field (provider default).
	out, err = rewriteRequestModel(body, "")
	if err != nil {
		t.Fatalf("rewrite empty: %v", err)
	}
	if strings.Contains(string(out), `"model"`) {
		t.Fatalf("expected model removed, got %s", out)
	}
}

func TestProxyGateAllowsGrantedRoute(t *testing.T) {
	p, st := newProxyTest(t)
	ctx := context.Background()
	c, _ := st.CreateClient(ctx, "acme", "")
	plaintext, _, _ := st.CreateToken(ctx, c.ID, "", time.Hour)
	if _, err := st.AddGrant(ctx, c.ID, "anthropic", "claude-x", ""); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}

	var (
		called   bool
		gotRoute proxyForcedRoute
		gotBody  []byte
	)
	h := p.gate(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotRoute, _ = proxyForcedRouteFromContext(r.Context())
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	body := `{"model":"anthropic/claude-x","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rr := httptest.NewRecorder()
	h(rr, req)

	if !called {
		t.Fatal("expected forwarded handler to be called")
	}
	if gotRoute.Provider != "anthropic" || gotRoute.Model != "claude-x" {
		t.Fatalf("forced route = %+v, want anthropic/claude-x", gotRoute)
	}
	if m := peekRequestModel(gotBody); m != "claude-x" {
		t.Fatalf("rewritten body model = %q, want concrete claude-x", m)
	}
}

func TestProxyGateDeniesUngrantedAndRecordsRequest(t *testing.T) {
	p, st := newProxyTest(t)
	ctx := context.Background()
	c, _ := st.CreateClient(ctx, "acme", "")
	plaintext, _, _ := st.CreateToken(ctx, c.ID, "", time.Hour)

	called := false
	h := p.gate(func(w http.ResponseWriter, r *http.Request) { called = true })

	body := `{"model":"openai/gpt-5","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rr := httptest.NewRecorder()
	h(rr, req)

	if called {
		t.Fatal("handler must not be reached for a denied request")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	var resp struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 403 body: %v", err)
	}
	if resp.Error.Code != "model_access_not_granted" || resp.Error.RequestID == "" {
		t.Fatalf("unexpected 403 payload: %+v", resp.Error)
	}
	if resp.Error.Status != proxy.RequestPending {
		t.Fatalf("status = %q", resp.Error.Status)
	}
	pending, _ := st.ListAccessRequests(ctx, proxy.RequestPending, c.ID)
	if len(pending) != 1 || pending[0].Provider != "openai" || pending[0].Model != "gpt-5" {
		t.Fatalf("expected 1 pending request for openai/gpt-5, got %+v", pending)
	}
}

func TestProxyGateAuthErrors(t *testing.T) {
	p, st := newProxyTest(t)
	ctx := context.Background()
	c, _ := st.CreateClient(ctx, "acme", "")
	plaintext, _, _ := st.CreateToken(ctx, c.ID, "", time.Hour)
	h := p.gate(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	// Missing token -> 401.
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"anthropic/claude-x"}`))
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rr.Code)
	}

	// Valid token but no model -> 400.
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rr = httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing model status = %d, want 400", rr.Code)
	}
}

func TestProxyGateWildcardGrant(t *testing.T) {
	p, st := newProxyTest(t)
	ctx := context.Background()
	c, _ := st.CreateClient(ctx, "acme", "")
	plaintext, _, _ := st.CreateToken(ctx, c.ID, "", time.Hour)
	if _, err := st.AddGrant(ctx, c.ID, "claude-bin", proxy.WildcardModel, ""); err != nil {
		t.Fatalf("AddGrant wildcard: %v", err)
	}

	capture := func(bodyStr string) (proxyForcedRoute, []byte, int) {
		var route proxyForcedRoute
		var gotBody []byte
		h := p.gate(func(w http.ResponseWriter, r *http.Request) {
			route, _ = proxyForcedRouteFromContext(r.Context())
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(bodyStr))
		req.Header.Set("Authorization", "Bearer "+plaintext)
		rr := httptest.NewRecorder()
		h(rr, req)
		return route, gotBody, rr.Code
	}

	// Concrete model under a wildcard grant routes to that concrete model.
	route, body, code := capture(`{"model":"claude-bin/claude-sonnet-4-6","messages":[]}`)
	if code != http.StatusOK {
		t.Fatalf("concrete under wildcard: code = %d", code)
	}
	if route.Provider != "claude-bin" || route.Model != "claude-sonnet-4-6" {
		t.Fatalf("concrete route = %+v", route)
	}
	if peekRequestModel(body) != "claude-sonnet-4-6" {
		t.Fatalf("concrete body model = %q", peekRequestModel(body))
	}

	// Literal wildcard request routes to the provider default (empty model).
	route, body, code = capture(`{"model":"claude-bin/*","messages":[]}`)
	if code != http.StatusOK {
		t.Fatalf("wildcard literal: code = %d", code)
	}
	if route.Provider != "claude-bin" || route.Model != "" {
		t.Fatalf("wildcard route = %+v, want claude-bin with empty model", route)
	}
	if peekRequestModel(body) != "" {
		t.Fatalf("wildcard body should drop model, got %q", peekRequestModel(body))
	}
}

func TestProxyAdminUIShellAndCatalog(t *testing.T) {
	p, _ := newProxyTest(t)
	h := p.handler(&serveServer{})

	for _, path := range []string{"/", "/proxy-admin.css", "/proxy-admin.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "admin-secret") {
			t.Fatalf("GET %s leaked the admin token", path)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/proxy/models", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("catalog without admin token = %d, want 401", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/proxy/models", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("catalog with admin token = %d: %s", rr.Code, rr.Body.String())
	}
	var catalog struct {
		Models []proxy.ModelAlias `json:"models"`
	}
	mustJSON(t, rr, &catalog)
	if len(catalog.Models) == 0 {
		t.Fatal("expected admin model catalog")
	}
}

func TestProxyAdminAndSelfServiceFlow(t *testing.T) {
	p, _ := newProxyTest(t)
	h := p.handler(&serveServer{})

	admin := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer admin-secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	client := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Admin without a token is rejected.
	if rr := (func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/admin/proxy/clients", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	})(); rr.Code != http.StatusUnauthorized {
		t.Fatalf("admin without token = %d, want 401", rr.Code)
	}

	// Create client.
	rr := admin(http.MethodPost, "/admin/proxy/clients", `{"name":"ci","description":"CI bot"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create client = %d: %s", rr.Code, rr.Body)
	}
	var createdClient struct {
		Client proxy.Client `json:"client"`
	}
	mustJSON(t, rr, &createdClient)
	cid := createdClient.Client.ID

	// Create token.
	rr = admin(http.MethodPost, "/admin/proxy/clients/"+cid+"/tokens", `{"ttl_seconds":3600}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token = %d: %s", rr.Code, rr.Body)
	}
	var tokResp struct {
		Token string `json:"token"`
	}
	mustJSON(t, rr, &tokResp)
	if tokResp.Token == "" {
		t.Fatal("expected plaintext token returned once")
	}

	// Grant a model.
	rr = admin(http.MethodPost, "/admin/proxy/clients/"+cid+"/grants", `{"provider":"anthropic","model":"claude-x"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create grant = %d: %s", rr.Code, rr.Body)
	}

	// Client sees the catalog with its grant marked.
	rr = client(http.MethodGet, "/v1/models", tokResp.Token, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("client models = %d: %s", rr.Code, rr.Body)
	}
	var models struct {
		Data []proxyModelEntry `json:"data"`
	}
	mustJSON(t, rr, &models)
	grantedSeen := false
	for _, m := range models.Data {
		if m.Provider == "anthropic" && m.Model == "claude-x" && m.Granted {
			grantedSeen = true
		}
	}
	if !grantedSeen {
		t.Fatal("expected anthropic/claude-x to be marked granted for the client")
	}

	// Client self-service requests access to a different model.
	rr = client(http.MethodPost, "/v1/proxy/access-requests", tokResp.Token, `{"model":"openai/gpt-5","reason":"need it"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("access request = %d: %s", rr.Code, rr.Body)
	}

	// Admin lists pending requests and approves it.
	rr = admin(http.MethodGet, "/admin/proxy/requests?status=pending", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list requests = %d: %s", rr.Code, rr.Body)
	}
	var reqs struct {
		AccessRequests []proxy.AccessRequest `json:"access_requests"`
	}
	mustJSON(t, rr, &reqs)
	if len(reqs.AccessRequests) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(reqs.AccessRequests))
	}
	reqID := reqs.AccessRequests[0].ID

	rr = admin(http.MethodPost, "/admin/proxy/requests/"+reqID+"/approve", `{"note":"ok"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", rr.Code, rr.Body)
	}

	// Approval created a grant: the client can now see it granted.
	rr = client(http.MethodGet, "/v1/models", tokResp.Token, "")
	mustJSON(t, rr, &models)
	approvedSeen := false
	for _, m := range models.Data {
		if m.Provider == "openai" && m.Model == "gpt-5" && m.Granted {
			approvedSeen = true
		}
	}
	if !approvedSeen {
		t.Fatal("expected openai/gpt-5 to be granted after approval")
	}

	// Audit trail is populated (grant/authorize actions recorded).
	rr = admin(http.MethodGet, "/admin/proxy/audit", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("audit = %d: %s", rr.Code, rr.Body)
	}
}

func mustJSON(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON %T: %v (body=%s)", dst, err, rr.Body)
	}
}
