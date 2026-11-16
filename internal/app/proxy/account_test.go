package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testAccount(t *testing.T, s *Service, kind string, up *httptest.Server, expired bool) accountRecord {
	t.Helper()
	expiry := time.Now().Add(time.Hour).Unix()
	if expired {
		expiry = time.Now().Add(-time.Hour).Unix()
	}
	a := accountRecord{AccountInfo: AccountInfo{Name: "account", Kind: kind, State: "ready", Generation: 1, ExpiresAt: expiry, Updated: time.Now().UnixMilli()}, credentials: accountCredentials{AccessToken: "synthetic-old-access", RefreshToken: "synthetic-old-refresh", AccountID: "synthetic-account-id", GitHubToken: "synthetic-github-token", ExpiresAt: expiry}, operator: accountOperator{Issuer: up.URL + "/oauth/token", Origins: []string{up.URL}}}
	if err := validateAccountOperator(a.operator); err != nil {
		t.Fatal(err)
	}
	s.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err := s.store.replaceAccount(context.Background(), a, 0); err != nil {
		t.Fatal(err)
	}
	s.rememberAccount(a.Name, a.credentials)
	return a
}
func accountRoute(up string) Route {
	return Route{Name: "account-route", LocalPrefix: "/account/", Upstream: up + "/api", Enabled: true, Account: "account", CallerAuth: Auth{Header: "Authorization", Prefix: "Bearer "}, Allow: []string{"POST /responses"}, RequestIDHeader: "X-Vendor-ID"}
}
func configureAccount(t *testing.T, s *Service, r Route) string {
	t.Helper()
	configure(t, s, Config{Routes: []Route{r}}, nil)
	if err := s.validateAccounts(s.snap); err != nil {
		t.Fatal(err)
	}
	return caller(t, s, r.Name)
}
func accountCall(s *Service, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/account/responses", strings.NewReader(`{"model":"fixture"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-ID", "worker-placeholder")
	req.Header.Set("X-API-Key", "worker-alternate-key")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}

func TestAccountConcurrentRefreshAndNoGlobalLock(t *testing.T) {
	var issuer, inference atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" || r.Form.Get("refresh_token") != "synthetic-old-refresh" || r.Form.Get("client_id") == "" || r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("bad refresh shape %v", r.Form)
			}
			once.Do(func() { close(entered) })
			<-release
			io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-new-access" || r.Header.Get("ChatGPT-Account-ID") != "synthetic-account-id" || r.Header.Get("X-API-Key") != "" {
			t.Errorf("account substitution: %v", r.Header)
		}
		w.Header().Set("X-Vendor-ID", "synthetic-new-access synthetic-old-refresh")
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 16)
	for range 16 {
		go func() { <-start; results <- accountCall(s, token) }()
	}
	close(start)
	<-entered
	// Metadata/admin remains available during a stalled account issuer call.
	metadataDone := make(chan int, 1)
	go func() {
		cookie, csrf := loginAdmin(t, s)
		metadataDone <- adminRequest(s, "GET", "state", nil, cookie, csrf).Code
	}()
	select {
	case code := <-metadataDone:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("issuer held the global configuration lock")
	}
	close(release)
	for range 16 {
		select {
		case rr := <-results:
			if rr.Code != 200 {
				t.Fatalf("response %d %s", rr.Code, rr.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("refresh waiters stuck")
		}
	}
	if issuer.Load() != 1 || inference.Load() != 16 {
		t.Fatalf("issuer=%d inference=%d", issuer.Load(), inference.Load())
	}
	a, err := s.store.account("account")
	if err != nil || a.Generation != 2 || a.credentials.RefreshToken != "synthetic-new-refresh" || a.State != "ready" {
		t.Fatalf("persisted rotation %+v %v", a.AccountInfo, err)
	}
	records, _, err := s.store.records(nil, 50)
	if err != nil || len(records) != 16 {
		t.Fatal(err, len(records))
	}
	for _, r := range records {
		if strings.Contains(r.UpstreamID, "synthetic-new-access") || strings.Contains(r.UpstreamID, "synthetic-old-refresh") {
			t.Fatal("account material in ledger")
		}
	}
	var durability int
	if err = s.store.db.QueryRow("PRAGMA synchronous").Scan(&durability); err != nil || durability != 1 {
		t.Fatalf("generic durability changed %d %v", durability, err)
	}
}

func TestAccountRejectedUnexpiredGenerationRefreshNoReplay(t *testing.T) {
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		if r.Header.Get("Authorization") == "Bearer synthetic-old-access" {
			w.WriteHeader(401)
			io.WriteString(w, "authentication rejected")
			return
		}
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	s.transport.MaxConnsPerHost = 1 // the 401 response must not deadlock its same-origin issuer
	testAccount(t, s, "chatgpt", up, false)
	token := configureAccount(t, s, accountRoute(up.URL))
	rr := accountCall(s, token)
	if rr.Code != 401 || rr.Header().Get("X-Term-LLM-Proxy-Upstream-Auth") != "refreshed" || issuer.Load() != 1 || inference.Load() != 1 {
		t.Fatalf("rejection: %d %v issuer=%d inference=%d", rr.Code, rr.Header(), issuer.Load(), inference.Load())
	}
	// Concurrent rejections of an older generation must adopt the committed result.
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			a, e := s.ensureAccount(context.Background(), "account", 1)
			if e != nil || a.Generation != 2 {
				t.Errorf("generation %d %v", a.Generation, e)
			}
		})
	}
	wg.Wait()
	rr = accountCall(s, token)
	if rr.Code != 200 || issuer.Load() != 1 || inference.Load() != 2 {
		t.Fatal("replayed inference or stale-generation refresh", rr.Code, issuer.Load(), inference.Load())
	}
}

func TestAccountFenceCommitFailureAndConsumedResultFailure(t *testing.T) {
	for _, phase := range []string{"attempt", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			var issuer, inference atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
					return
				}
				inference.Add(1)
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, "chatgpt", up, true)
			token := configureAccount(t, s, accountRoute(up.URL))
			condition := "NEW.state='refreshing'"
			if phase == "replacement" {
				condition = "NEW.generation>OLD.generation"
			}
			if _, e := s.store.db.Exec("CREATE TRIGGER fail_account BEFORE UPDATE ON accounts WHEN " + condition + " BEGIN SELECT RAISE(FAIL,'synthetic custody commit failure'); END"); e != nil {
				t.Fatal(e)
			}
			rr := accountCall(s, token)
			if rr.Code != 502 || inference.Load() != 0 {
				t.Fatal("dispatched without custody commit", rr.Code, inference.Load())
			}
			want := int32(0)
			if phase == "replacement" {
				want = 1
			}
			if issuer.Load() != want {
				t.Fatal("issuer consumed before fence", issuer.Load())
			}
			if _, e := s.store.db.Exec("DROP TRIGGER fail_account"); e != nil {
				t.Fatal(e)
			}
			if phase == "replacement" {
				if rr = accountCall(s, token); rr.Code != 502 || issuer.Load() != 1 {
					t.Fatal("consumed refresh token reused")
				}
				opts := s.opts
				s.Close()
				next, e := Open(opts)
				if e != nil {
					t.Fatal(e)
				}
				defer next.Close()
				a, e := next.store.account("account")
				if e != nil || a.State != "needs_login" {
					t.Fatal("uncertain account resurrected after restart", a.State, e)
				}
			} else {
				if rr = accountCall(s, token); rr.Code != 200 || issuer.Load() != 1 {
					t.Fatal("unconsumed refresh could not retry", rr.Code, issuer.Load())
				}
			}
		})
	}
}

func TestAccountIssuerFailureAndRedirectNeverLeaksOrReuses(t *testing.T) {
	for _, mode := range []string{"rejected", "redirect", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			var issuer, destination atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1) }))
			defer target.Close()
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				issuer.Add(1)
				switch mode {
				case "redirect":
					w.Header().Set("Location", target.URL)
					w.WriteHeader(307)
				case "malformed":
					io.WriteString(w, "synthetic-issuer-secret")
				default:
					w.WriteHeader(400)
					io.WriteString(w, `{"error":"synthetic-issuer-secret","refresh_token":"synthetic-old-refresh"}`)
				}
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, "chatgpt", up, true)
			token := configureAccount(t, s, accountRoute(up.URL))
			for range 2 {
				rr := accountCall(s, token)
				if rr.Code != 502 || strings.Contains(rr.Body.String(), "synthetic-") {
					t.Fatalf("issuer error exposed: %d %s", rr.Code, rr.Body.String())
				}
			}
			if issuer.Load() != 1 || destination.Load() != 0 {
				t.Fatal("issuer followed redirect or reused consumed token", issuer.Load(), destination.Load())
			}
		})
	}
}

func TestAccountOriginAndPolicyFailBeforeIssuerOrInference(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "chatgpt", up, true)
	// Remove the fixture override: a production ChatGPT credential cannot be sent
	// to a loopback route, even if a stale or bypassed config snapshot reaches RT.
	a.operator.Origins = nil
	if err := s.store.replaceAccount(context.Background(), a, 1); err != nil {
		t.Fatal(err)
	}
	r := accountRoute(up.URL)
	configure(t, s, Config{Routes: []Route{r}}, nil)
	token := caller(t, s, r.Name)
	if rr := accountCall(s, token); rr.Code != 502 {
		t.Fatal(rr.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("credential used on wrong origin")
	}
	if err := s.validateAccounts(s.snap); err == nil {
		t.Fatal("wrong-origin config activated")
	}
	a.operator.Origins = []string{up.URL}
	if err := s.store.replaceAccount(context.Background(), a, 1); err != nil {
		t.Fatal(err)
	}
	r.Allow = []string{"GET /models"}
	configure(t, s, Config{Routes: []Route{r}}, nil)
	token = caller(t, s, r.Name)
	if rr := accountCall(s, token); rr.Code != 403 || calls.Load() != 0 {
		t.Fatal("disallowed path reached issuer", rr.Code, calls.Load())
	}
}

func TestAccountPublicMetadataAndWhoamiNeverExportSecrets(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "chatgpt", up, false)
	token := configureAccount(t, s, accountRoute(up.URL))
	cookie, csrf := loginAdmin(t, s)
	for _, path := range []string{"state", "accounts", "export"} {
		rr := adminRequest(s, "GET", path, nil, cookie, csrf)
		if rr.Code != 200 {
			t.Fatal(path, rr.Code)
		}
		for _, secret := range append(accountValues(a.credentials), a.operator.Issuer) {
			if secret != "" && strings.Contains(rr.Body.String(), secret) {
				t.Fatalf("%s exported material", path)
			}
		}
	}
	req := httptest.NewRequest("GET", "/_proxy/client/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "account-route") || strings.Contains(rr.Body.String(), a.credentials.AccessToken) {
		t.Fatal("whoami", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/_proxy/client/v1/whoami", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatal("whoami accepted browser cookie")
	}
	// Issuer/origin overrides are not fields in write-only admin input.
	for _, field := range []string{"issuer", "origins", "client_id"} {
		payload := map[string]any{"name": "other", "kind": "chatgpt", "credentials": a.credentials, field: "https://attacker.invalid"}
		rr = adminRequest(s, "POST", "accounts", payload, cookie, csrf)
		if rr.Code != 400 {
			t.Fatal("UI accepted operator-only override", field, rr.Code)
		}
	}
	// Sign-out is durable and closes admission, but does not delete route history.
	rr = adminRequest(s, "DELETE", "accounts", map[string]string{"name": "account"}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if rr = accountCall(s, token); rr.Code != 502 {
		t.Fatal("signed-out account dispatched", rr.Code)
	}
}

func TestAccountBootstrapPrivateJSONRoundTrip(t *testing.T) {
	// Verify the promoted private JSON types intentionally accept only the owned
	// credential/operator fields; no token response or arbitrary header map.
	var b accountBootstrap
	raw := `{"access_token":"access","refresh_token":"refresh","account_id":"identity","expires_at":123,"issuer":"https://fixture.invalid/token","origins":["https://fixture.invalid"],"client_id":"client"}`
	if e := json.Unmarshal([]byte(raw), &b); e != nil || b.AccessToken != "access" || b.Issuer == "" {
		t.Fatal(e, fmt.Sprintf("%+v", b.accountOperator))
	}
}

func TestAccountActualCommitFailureFenceAndReplacement(t *testing.T) {
	for _, phase := range []string{"attempt", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, "chatgpt", up, true)
			token := configureAccount(t, s, accountRoute(up.URL))
			condition := "NEW.state='refreshing'"
			if phase == "replacement" {
				condition = "NEW.generation>OLD.generation"
			}
			// This constraint is deferred: the UPDATE succeeds, and COMMIT itself fails.
			_, err := s.store.db.Exec("PRAGMA foreign_keys=ON; CREATE TABLE custody_parent(id INTEGER PRIMARY KEY); CREATE TABLE custody_child(parent INTEGER REFERENCES custody_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_commit AFTER UPDATE ON accounts WHEN " + condition + " BEGIN INSERT INTO custody_child VALUES(123); END;")
			if err != nil {
				t.Fatal(err)
			}
			rr := accountCall(s, token)
			if rr.Code != 502 {
				t.Fatal(rr.Code)
			}
			want := int32(0)
			if phase == "replacement" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("issuer consumption crossed failed commit", calls.Load())
			}
			var pending int
			if err = s.store.db.QueryRow("SELECT count(*) FROM custody_child").Scan(&pending); err != nil || pending != 0 {
				t.Fatal("failed COMMIT left an open transaction", err, pending)
			}
			if _, err = s.store.db.Exec("DROP TRIGGER fail_commit"); err != nil {
				t.Fatal(err)
			}
			if phase == "replacement" {
				if rr = accountCall(s, token); rr.Code != 502 || calls.Load() != 1 {
					t.Fatal("consumed refresh reused after failed COMMIT")
				}
			} else {
				if rr = accountCall(s, token); rr.Code != 200 || calls.Load() != 2 {
					t.Fatal("unconsumed refresh could not retry after failed COMMIT", rr.Code, calls.Load())
				}
			}
		})
	}
}
