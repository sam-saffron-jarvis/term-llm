package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func installTokenOnlyAccount(t *testing.T, s *Service, up *httptest.Server, expiry int64) accountRecord {
	t.Helper()
	a := accountRecord{AccountInfo: AccountInfo{Name: "account", Kind: "claude-code", State: "ready", Generation: 1, ExpiresAt: expiry, Updated: time.Now().UnixMilli()}, credentials: accountCredentials{Mode: "token_only", AccessToken: "synthetic-token-only-access", ExpiresAt: expiry}, operator: accountOperator{Issuer: up.URL + "/oauth/token", Origins: []string{up.URL}}}
	if err := validateAccountCredentials(a.Kind, a.credentials); err != nil {
		t.Fatal(err)
	}
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

func TestAccountTokenOnlyCredentialValidation(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	for _, tc := range []struct {
		name, kind  string
		credentials accountCredentials
		valid       bool
	}{
		{"unknown expiry", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic"}, true},
		{"known expiry", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", ExpiresAt: future}, true},
		{"expired shape is valid", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", ExpiresAt: 1}, true},
		{"legacy rotating", "claude-code", accountCredentials{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: future}, true},
		{"explicit rotating", "claude-code", accountCredentials{Mode: "rotating", AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: future}, true},
		{"refresh forbidden", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", RefreshToken: "synthetic-refresh"}, false},
		{"other credential forbidden", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", GitHubToken: "synthetic-other"}, false},
		{"bare token required", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "Bearer synthetic"}, false},
		{"empty token", "claude-code", accountCredentials{Mode: "token_only"}, false},
		{"API key is not OAuth", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "sk-ant-api03-synthetic"}, false},
		{"scoped proxy token is not OAuth", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "sk-tlp-synthetic"}, false},
		{"admin token is not OAuth", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "admin_synthetic"}, false},
		{"negative expiry", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", ExpiresAt: -1}, false},
		{"millisecond expiry", "claude-code", accountCredentials{Mode: "token_only", AccessToken: "synthetic", ExpiresAt: time.Now().UnixMilli()}, false},
		{"unknown mode", "claude-code", accountCredentials{Mode: "token-only", AccessToken: "synthetic"}, false},
		{"missing explicit mode", "claude-code", accountCredentials{AccessToken: "synthetic"}, false},
		{"rotating lacks expiry", "claude-code", accountCredentials{Mode: "rotating", AccessToken: "synthetic", RefreshToken: "synthetic-refresh"}, false},
		{"rotating lacks refresh", "claude-code", accountCredentials{Mode: "rotating", AccessToken: "synthetic", ExpiresAt: future}, false},
		{"Claude only", "chatgpt", accountCredentials{Mode: "token_only", AccessToken: "synthetic"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAccountCredentials(tc.kind, tc.credentials)
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestAccountTokenOnlyUnknownAndNearExpiryNeverRefresh(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "unknown"
		if known {
			name = "near-expiry"
		}
		t.Run(name, func(t *testing.T) {
			var issuer, inference atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					w.WriteHeader(500)
					return
				}
				inference.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-token-only-access" || !headerHasToken(r.Header, "Anthropic-Beta", "oauth-2025-04-20") || !headerHasToken(r.Header, "Anthropic-Beta", "fixture-beta") || r.Header.Get("X-API-Key") != "" {
					t.Error("token-only auth/header contract changed")
				}
				w.Header().Set("X-Vendor-ID", "synthetic-token-only-access")
				io.WriteString(w, "unchanged response")
			}))
			defer up.Close()
			s := testService(t)
			expiry := int64(0)
			if known {
				expiry = time.Now().Add(3 * time.Minute).Unix()
			}
			installTokenOnlyAccount(t, s, up, expiry)
			token := configureAccount(t, s, accountRoute(up.URL))
			for range 2 {
				req := httptest.NewRequest("POST", "/account/responses", strings.NewReader("synthetic body"))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Anthropic-Beta", "fixture-beta")
				req.Header.Set("X-API-Key", "worker-placeholder")
				rr := httptest.NewRecorder()
				s.ServeHTTP(rr, req)
				if rr.Code != 200 || rr.Body.String() != "unchanged response" {
					t.Fatal(rr.Code, rr.Body.String())
				}
			}
			a, err := s.store.account("account")
			if err != nil || a.AuthMode != "token_only" || a.ExpiresAt != expiry || a.credentials.ExpiresAt != expiry || a.Generation != 1 || a.State != "ready" {
				t.Fatal("token-only lifetime or mode changed", a.AccountInfo, err)
			}
			if _, err = s.exchangeAccount(context.Background(), a); err == nil {
				t.Fatal("direct token-only exchange was accepted")
			}
			if issuer.Load() != 0 || inference.Load() != 2 {
				t.Fatal("unexpected refresh or replay", issuer.Load(), inference.Load())
			}
			records, _, err := s.store.records(nil, 5)
			if err != nil || len(records) != 2 {
				t.Fatal(err)
			}
			for _, rec := range records {
				if strings.Contains(rec.UpstreamID, a.credentials.AccessToken) {
					t.Fatal("token-only access leaked to ledger")
				}
			}
			infos, err := s.store.accountInfos()
			if err != nil || len(infos) != 1 || infos[0].AuthMode != "token_only" {
				t.Fatal("public auth mode missing", infos, err)
			}
			data, _ := json.Marshal(infos)
			if strings.Contains(string(data), a.credentials.AccessToken) || strings.Contains(string(data), "issuer") {
				t.Fatal("public metadata exported credentials")
			}
			if !known && strings.Contains(string(data), "expires_at") {
				t.Fatal("unknown expiry was invented")
			}
		})
	}
}

func TestAccountTokenOnlyKnownExpiryFailsClosedWithoutIssuer(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	s := testService(t)
	installTokenOnlyAccount(t, s, up, time.Now().Add(-time.Second).Unix())
	token := configureAccount(t, s, accountRoute(up.URL))
	if rr := accountCall(s, token); rr.Code != 502 {
		t.Fatal(rr.Code)
	}
	a, err := s.store.account("account")
	if err != nil || a.State != "needs_login" || calls.Load() != 0 {
		t.Fatal("expired token-only account dispatched", a.AccountInfo, err, calls.Load())
	}
	var state string
	if err = s.store.db.QueryRow("SELECT state FROM accounts WHERE name='account'").Scan(&state); err != nil || state != "needs_login" {
		t.Fatal("expiry was not persisted", state, err)
	}
}

func TestAccountTokenOnly401PreservesOriginalAndReconnectPreservesGrant(t *testing.T) {
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			w.WriteHeader(500)
			return
		}
		inference.Add(1)
		if r.Header.Get("Authorization") == "Bearer synthetic-token-only-access" {
			w.Header().Set("X-Original", "kept")
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"synthetic rejected token"}`)
			return
		}
		io.WriteString(w, "explicit replacement accepted")
	}))
	defer up.Close()
	s := testService(t)
	installTokenOnlyAccount(t, s, up, 0)
	token := configureAccount(t, s, accountRoute(up.URL))
	authority := s.snap.routes[0].authority
	rr := accountCall(s, token)
	if rr.Code != 401 || rr.Body.String() != `{"error":"synthetic rejected token"}` || rr.Header().Get("X-Original") != "kept" || rr.Header().Get("X-Term-LLM-Proxy-Upstream-Auth") != "login-required" {
		t.Fatal("original 401 was changed/retried", rr.Code, rr.Body.String())
	}
	a, err := s.store.account("account")
	if err != nil || a.State != "needs_login" || a.AuthMode != "token_only" || a.refresh.RejectedGeneration != 1 {
		t.Fatal("401 was not durable needs_login", a.AccountInfo, err)
	}
	if rr = accountCall(s, token); rr.Code != 502 || inference.Load() != 1 || issuer.Load() != 0 {
		t.Fatal("token-only rejection replayed", rr.Code, inference.Load(), issuer.Load())
	}
	cookie, csrf := loginAdmin(t, s)
	rr = adminRequest(s, "PUT", "accounts", accountInput{Name: "account", Kind: "claude-code", Replace: true, Credentials: accountCredentials{Mode: "token_only", AccessToken: "synthetic-explicit-replacement"}}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal("explicit reconnect failed", rr.Code, rr.Body.String())
	}
	if s.snap.routes[0].authority != authority {
		t.Fatal("same-account reconnect changed route authority")
	}
	if _, err = s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("same-account reconnect removed grant", err)
	}
	if rr = accountCall(s, token); rr.Code != 200 || inference.Load() != 2 || issuer.Load() != 0 {
		t.Fatal("replacement not used exactly once", rr.Code, inference.Load(), issuer.Load())
	}
}

func TestAccountTokenOnlyRejectionSurvivesStorageFailure(t *testing.T) {
	for _, allWrites := range []bool{false, true} {
		name := "state-column"
		if allWrites {
			name = "all-writes"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(401)
				io.WriteString(w, "original rejected response")
			}))
			defer up.Close()
			s := testService(t)
			installTokenOnlyAccount(t, s, up, 0)
			token := configureAccount(t, s, accountRoute(up.URL))
			condition := "NEW.state='needs_login'"
			if allWrites {
				condition = "1=1"
			}
			if _, err := s.store.db.Exec("CREATE TRIGGER reject_token_only_state BEFORE UPDATE ON accounts WHEN " + condition + " BEGIN SELECT RAISE(FAIL,'synthetic state storage failure'); END"); err != nil {
				t.Fatal(err)
			}
			if rr := accountCall(s, token); rr.Code != 401 || rr.Body.String() != "original rejected response" {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if rr := accountCall(s, token); rr.Code != 502 || calls.Load() != 1 {
				t.Fatal("storage failure re-enabled rejected token", rr.Code, calls.Load())
			}
			if !allWrites {
				a, err := s.store.account("account")
				if err != nil || a.State != "needs_login" || a.refresh.RejectedGeneration != 1 {
					t.Fatal("rejection proof did not imply needs_login", a.AccountInfo, err)
				}
				opts := s.opts
				s.Close()
				next, err := Open(opts)
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				if rr := accountCall(next, token); rr.Code != 502 || calls.Load() != 1 {
					t.Fatal("restart ignored durable token-only rejection", rr.Code, calls.Load())
				}
			} else {
				if _, err := s.store.db.Exec("DROP TRIGGER reject_token_only_state"); err != nil {
					t.Fatal(err)
				}
				if rr := accountCall(s, token); rr.Code != 502 || calls.Load() != 1 {
					t.Fatal("recovered storage allowed old token", rr.Code, calls.Load())
				}
				a, err := s.store.account("account")
				if err != nil || a.State != "needs_login" {
					t.Fatal("recovered storage did not persist needs_login", a.AccountInfo, err)
				}
			}
		})
	}
}

func TestAccountTokenOnlyLegacyRotatingRemainsRenewable(t *testing.T) {
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			io.WriteString(w, `{"access_token":"synthetic-renewed","refresh_token":"synthetic-renewed-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-renewed" {
			t.Error("legacy renewal lost")
		}
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "claude-code", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	a, err := s.store.account("account")
	if err != nil || a.AuthMode != "rotating" || a.credentials.Mode != "" {
		t.Fatal("legacy mode was changed", a.AccountInfo, err)
	}
	if rr := accountCall(s, token); rr.Code != 200 || issuer.Load() != 1 || inference.Load() != 1 {
		t.Fatal("legacy credential stopped rotating", rr.Code, issuer.Load(), inference.Load())
	}
}

func TestAccountTokenOnlyOptionalOAuthScope401DoesNotSignOutInference(t *testing.T) {
	for _, path := range []string{"/api/oauth/usage", "/api/oauth/profile"} {
		t.Run(path, func(t *testing.T) {
			var calls, issuer atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					w.WriteHeader(500)
					return
				}
				calls.Add(1)
				w.WriteHeader(401)
				io.WriteString(w, "synthetic optional scope unavailable")
			}))
			defer up.Close()
			s := testService(t)
			installTokenOnlyAccount(t, s, up, 0)
			route := Route{Name: "optional", LocalPrefix: "/claude/", Upstream: up.URL, Enabled: true, Account: "account", CallerAuth: Auth{Header: "Authorization", Prefix: "Bearer "}, Allow: []string{"GET " + path}}
			token := configureAccount(t, s, route)
			req := httptest.NewRequest("GET", "/claude"+path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			account, err := s.store.account("account")
			if rr.Code != 401 || rr.Body.String() != "synthetic optional scope unavailable" || rr.Header().Get("X-Term-LLM-Proxy-Upstream-Auth") != "scope-rejected" || err != nil || account.State != "ready" || account.refresh.RejectedGeneration != 0 || calls.Load() != 1 || issuer.Load() != 0 {
				t.Fatal("optional-scope rejection destroyed inference credential", rr.Code, account.AccountInfo, err, calls.Load(), issuer.Load())
			}
		})
	}
}
