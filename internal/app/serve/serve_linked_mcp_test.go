package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/mcp"
)

func peerMCPRequest(h http.HandlerFunc, method, path, body, id string, manage bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, linkeddevices.WithDevicePrincipal(r, linkeddevices.DevicePrincipal{DeviceID: id, Scope: linkeddevices.ScopeWebV1, MCPGrantLifetime: context.Background(), ManageMCP: manage}))
	return w
}

func TestLinkedMCPSelectionPromotesWorkingTool(t *testing.T) {
	writeServeMCPConfig(t, map[string]mcp.ServerConfig{"private": {Command: "private-path"}, "sample": {Command: os.Args[0], Env: map[string]string{runServeMCPHandlerTestServerEnv: "1"}, LinkedDevices: true}})
	srv, created := newServeMCPHandlerTestServer(t, newServeMCPTestStore(t))
	const path = "/v1/sessions/draft_sample/mcp"
	call := func(method, body string, want int) *httptest.ResponseRecorder {
		w := peerMCPRequest(srv.handleSessionByID, method, path, body, "peer", false)
		if w.Code != want {
			t.Fatalf("%s -> %d: %s", method, w.Code, w.Body)
		}
		return w
	}
	got := call("GET", "", 200)
	if *created != nil || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "auth_") || !strings.Contains(got.Body.String(), "sample") {
		t.Fatal(got.Body)
	}
	call("PATCH", `{"enabled":["private"]}`, 403)
	call("PATCH", `{"enabled":[],"command":"unknown"}`, 400)
	if *created != nil {
		t.Fatal("read or denied selection warmed runtime")
	}
	call("PATCH", `{"enabled":["sample"]}`, 200)
	if err := srv.promoteDraftMCPSelection(t.Context(), "draft_sample", "sample_session"); err != nil {
		t.Fatal(err)
	}
	rt, _, err := srv.runtimeForRequest(t.Context(), "sample_session")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := rt.engine.Tools().Get("sample__greet")
	if !ok {
		t.Fatal("promoted conversation has no tool")
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"name":"test"}`)); err != nil {
		t.Fatal(err)
	}
	list := func() *httptest.ResponseRecorder {
		return peerMCPRequest(srv.handleSessionByID, "GET", "/v1/sessions/sample_session/mcp", "", "peer", false)
	}
	if w := list(); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatalf("active shared server: %d %s", w.Code, w.Body)
	}
	req := linkeddevices.WithDevicePrincipal(httptest.NewRequest("POST", "/v1/responses", nil), linkeddevices.DevicePrincipal{DeviceID: "peer", Scope: linkeddevices.ScopeWebV1})
	peer, _ := linkeddevices.DeviceFromRequest(req)
	guard := linkedMCPRunGuard(peer, rt, nil)
	if err := guard(&core.Request{}); err != nil {
		t.Fatal(err)
	}
	if err := mcp.UpdateConfig(func(c *mcp.Config) error {
		server := c.Servers["sample"]
		server.LinkedDevices = false
		c.Servers["sample"] = server
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if w := list(); w.Code != 409 || strings.Contains(w.Body.String(), "sample") {
		t.Fatalf("unshared active selection: %d %s", w.Code, w.Body)
	}
	if err := guard(&core.Request{}); err == nil {
		t.Fatal("linked response accepted revoked selection")
	}
	if err := mcp.UpdateConfig(func(c *mcp.Config) error {
		server := c.Servers["sample"]
		server.LinkedDevices = true
		c.Servers["sample"] = server
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := peerMCPRequest(srv.handleSessionByID, "PATCH", "/v1/sessions/sample_session/mcp", `{"enabled":[]}`, "peer", false)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if _, ok := rt.engine.Tools().Get("sample__greet"); ok {
		t.Fatal("disabled tool retained")
	}
}

func TestLinkedMCPManagerRemoteInstallIsolation(t *testing.T) {
	writeServeMCPConfig(t, map[string]mcp.ServerConfig{"host": {Type: "http", URL: "https://host.example/tools", Headers: map[string]string{"Authorization": "secret"}}})
	srv, _ := newServeMCPHandlerTestServer(t, newServeMCPTestStore(t))
	const payload = `{"kind":"url","url":"https://remote.example/mcp","name":"remote"}`
	install := func(body string, manage bool, want int) {
		t.Helper()
		w := peerMCPRequest(srv.handleMCPServers, "POST", "/v1/mcp/servers", body, "one", manage)
		if w.Code != want || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("install response: %d %s", w.Code, w.Body)
		}
	}
	install(payload, false, 403)
	for _, body := range []string{`{"kind":"command","command":"not-allowed"}`, `{"kind":"url","url":"http://localhost/x"}`, `{"kind":"url","url":"https://127.0.0.1/x"}`, `{"kind":"url","url":"https://remote.example/mcp","headers":{"Authorization":"secret"}}`, `{"kind":"catalogue","catalogue_id":"bundled:filesystem"}`} {
		install(body, true, 400)
	}
	install(payload, true, 201)
	install(payload, true, 409)
	for _, tc := range []struct {
		id                   string
		manage, remote, host bool
	}{{"one", true, true, false}, {"two", true, false, false}, {"one", false, false, false}} {
		w := peerMCPRequest(srv.handleSessionByID, "GET", "/v1/sessions/draft_test/mcp", "", tc.id, tc.manage)
		if strings.Contains(w.Body.String(), `"name":"remote"`) != tc.remote || strings.Contains(w.Body.String(), `"name":"host"`) != tc.host || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Body)
		}
	}
	for _, tc := range []struct {
		id, name string
		manage   bool
		code     int
	}{{"one", "host", true, 403}, {"two", "remote", true, 403}, {"one", "remote", false, 403}, {"one", "remote", true, 409}} {
		w := peerMCPRequest(srv.handleSessionByID, "POST", "/v1/sessions/draft_test/mcp/"+tc.name+"/oauth/start", "{}", tc.id, tc.manage)
		if w.Code != tc.code {
			t.Fatalf("OAuth: %d %s", w.Code, w.Body)
		}
	}
	for _, tc := range []struct {
		id, name string
		code     int
	}{{"two", "remote", 403}, {"one", "host", 403}, {"one", "remote", 200}} {
		w := peerMCPRequest(srv.handleMCPServerByName, "DELETE", "/v1/mcp/servers/"+tc.name, "", tc.id, true)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "config") {
			t.Fatalf("remove: %d %s", w.Code, w.Body)
		}
	}
}

func TestLinkedMCPExpiredGrantUsesOnlySharedView(t *testing.T) {
	writeServeMCPConfig(t, map[string]mcp.ServerConfig{
		"shared":       {Command: "unused", LinkedDevices: true},
		"installation": {Type: "http", URL: "https://example.test", LinkedInstaller: "one"},
	})
	srv, _ := newServeMCPHandlerTestServer(t, newServeMCPTestStore(t))
	lifetime, end := context.WithCancel(t.Context())
	end()
	get := func(path string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler(w, linkeddevices.WithDevicePrincipal(req, linkeddevices.DevicePrincipal{
			DeviceID: "one", Scope: linkeddevices.ScopeWebV1, ManageMCP: true, MCPGrantLifetime: lifetime,
		}))
		return w
	}
	if w := get("/v1/sessions/draft_test/mcp", srv.handleSessionByID); w.Code != 200 || !strings.Contains(w.Body.String(), `"shared"`) || strings.Contains(w.Body.String(), `"installation"`) {
		t.Fatalf("expired selection: %d %s", w.Code, w.Body)
	}
	if w := get("/v1/mcp/oauth/flows/test", srv.handleMCPOAuthFlow); w.Code != 403 {
		t.Fatalf("expired flow lookup: %d", w.Code)
	}
}
