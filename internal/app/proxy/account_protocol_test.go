package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/awsauth"
	"github.com/term-llm/term-llm/internal/oauth"
)

func TestAccountRefreshProtocolKinds(t *testing.T) {
	for _, kind := range []string{"chatgpt", "grok", "copilot", "claude-code"} {
		t.Run(kind, func(t *testing.T) {
			var issuer, inference atomic.Int32
			var upstreamURL string
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					switch kind {
					case "copilot":
						if r.Method != "GET" || r.Header.Get("Authorization") != "token synthetic-github-token" || r.Header.Get("Editor-Version") == "" {
							t.Error("incorrect Copilot exchange")
						}
						json.NewEncoder(w).Encode(map[string]any{"token": "synthetic-new-access", "expires_at": time.Now().Add(time.Hour).Unix(), "endpoints": map[string]string{"api": upstreamURL}})
					case "claude-code":
						var form map[string]string
						if json.NewDecoder(r.Body).Decode(&form) != nil || r.Header.Get("Content-Type") != "application/json" || form["client_id"] != claudeClientID || form["grant_type"] != "refresh_token" || !strings.Contains(form["scope"], "user:inference") {
							t.Error("incorrect source-backed Claude refresh")
						}
						io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600,"scope":"user:inference user:profile"}`)
					default:
						_ = r.ParseForm()
						client := oauth.ChatGPTClientID
						if kind == "grok" {
							client = oauth.GrokClientID
						}
						if r.Method != "POST" || r.Form.Get("client_id") != client || r.Form.Get("refresh_token") != "synthetic-old-refresh" {
							t.Error("incorrect OAuth exchange")
						}
						io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
					}
					return
				}
				inference.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-new-access" {
					t.Error("missing renewed bearer")
				}
				switch kind {
				case "grok":
					if r.Header.Get("X-Userid") != "synthetic-account-id" || r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" {
						t.Error("Grok identity")
					}
				case "claude-code":
					if !headerHasToken(r.Header, "Anthropic-Beta", "oauth-2025-04-20") || !headerHasToken(r.Header, "Anthropic-Beta", "preserved-beta") {
						t.Error("Claude beta contract")
					}
				case "copilot":
					if r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
						t.Error("Copilot client contract")
					}
				}
				io.WriteString(w, "ok")
			}))
			defer up.Close()
			upstreamURL = up.URL
			s := testService(t)
			testAccount(t, s, kind, up, true)
			token := configureAccount(t, s, accountRoute(up.URL))
			req := httptest.NewRequest("POST", "/account/responses", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Userid", "placeholder")
			req.Header.Set("Anthropic-Beta", "preserved-beta")
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			if rr.Code != 200 || issuer.Load() != 1 || inference.Load() != 1 {
				t.Fatal("protocol", rr.Code, issuer.Load(), inference.Load())
			}
		})
	}
}

func TestAccountCopilotReturnedOriginIsNotAuthority(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"token":"synthetic-new-access","expires_at":4102444800,"endpoints":{"api":"https://attacker.invalid"}}`)
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "copilot", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	for range 2 {
		if rr := accountCall(s, token); rr.Code != 502 {
			t.Fatal(rr.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("untrusted returned origin retried")
	}
}

func TestAccountAWSSignsRewrittenBoundedBody(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"synthetic"}]}`)
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, payload) {
			t.Error("signer changed body")
		}
		if r.URL.EscapedPath() != "/model/arn%3Aaws%3Abedrock%3Aus-west-2%3A1%3Aapplication-inference-profile%2Fx/invoke" {
			t.Error("signer changed escaped resource", r.URL.EscapedPath())
		}
		if r.Header.Get("X-Amz-Security-Token") != "synthetic-session" || r.Header.Get("X-Amz-Credential") != "" {
			t.Error("worker signing identity retained")
		}
		expected := r.Clone(r.Context())
		expected.URL.Scheme = "https"
		expected.URL.Host = r.Host
		expected.Header.Del("Authorization")
		// net/http adds Accept-Encoding only after RoundTrip signing. DisableCompression
		// ensures the fixture receives the same signed header set.
		stamp, e := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		if e != nil {
			t.Error(e)
			return
		}
		sum := sha256.Sum256(payload)
		e = awsauth.SignV4(expected, hex.EncodeToString(sum[:]), awsauth.Credentials{AccessKeyID: "synthetic-access-key", SecretAccessKey: "synthetic-secret-key", SessionToken: "synthetic-session"}, "bedrock", "us-west-2", stamp)
		if e != nil || expected.Header.Get("Authorization") != r.Header.Get("Authorization") {
			t.Error("signature does not cover rewritten target/body")
		}
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "aws-sigv4", up, false)
	a.operator.Region = "us-west-2"
	a.credentials = accountCredentials{AccessKeyID: "synthetic-access-key", SecretAccessKey: "synthetic-secret-key", SessionToken: "synthetic-session", ExpiresAt: a.ExpiresAt}
	if e := s.store.replaceAccount(context.Background(), a, 1); e != nil {
		t.Fatal(e)
	}
	r := accountRoute(up.URL)
	r.Upstream = up.URL
	r.Allow = []string{"POST /model/"}
	token := configureAccount(t, s, r)
	path := "/account/model/arn%3Aaws%3Abedrock%3Aus-west-2%3A1%3Aapplication-inference-profile%2Fx/invoke"
	req := httptest.NewRequest("POST", path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Amz-Security-Token", "placeholder")
	req.Header.Set("X-Amz-Credential", "worker-credential")
	req.Header.Set("X-Amz-Date", "20000101T000000Z")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 || calls.Load() != 1 {
		t.Fatalf("AWS request %d %s calls=%d", rr.Code, rr.Body.String(), calls.Load())
	}
	// Unknown-length bodies are bounded before any dial, not signed incrementally.
	s.opts.MaxBody = 32
	req = httptest.NewRequest("POST", path, strings.NewReader(strings.Repeat("x", 64)))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 502 || calls.Load() != 1 {
		t.Fatal("oversized signing body dispatched", rr.Code, calls.Load())
	}
	for _, bad := range []string{"/model/%252e%252e/invoke", "/model/thing/../invoke", "/model/thing/delete", "/model/arn%253Aaws/invoke", "/%6dodel/thing/invoke"} {
		if validAWSModelPath(bad) {
			t.Errorf("accepted ambiguous model scope %s", bad)
		}
	}
}

func TestAccountWaiterCancellationAndMissingAccount(t *testing.T) {
	s := testService(t)
	unlock, err := s.accountLock(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, e := s.ensureAccount(ctx, "account", 0); done <- e }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("canceled waiter succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter retained admission")
	}
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	r := accountRoute(up.URL)
	configure(t, s, Config{Routes: []Route{r}}, nil)
	token := caller(t, s, r.Name)
	if rr := accountCall(s, token); rr.Code != 502 || calls.Load() != 0 {
		t.Fatal("missing account dispatched", rr.Code, calls.Load())
	}
}

func TestAccountFreshPasskeyMutation(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	_, assert := bootstrapPasskey(t, b)
	// Bootstrap grants no fresh security-change assertion.
	in := accountInput{Name: "fresh", Kind: "copilot", Credentials: accountCredentials{GitHubToken: "synthetic-github-token"}}
	b.send("POST", "accounts", in, 403)
	assert("reauth", 1)
	b.send("POST", "accounts", in, 201)
	in.Replace = true
	b.send("PUT", "accounts", in, 403)
	assert("reauth", 2)
	b.send("PUT", "accounts", in, 200)
	b.send("DELETE", "accounts", map[string]string{"name": "fresh"}, 403)
}

func TestAccountAttributionBoundsAndDuplicateIdentity(t *testing.T) {
	if got := boundedAttribution("\x00\n" + strings.Repeat("é", 80)); len(got) != 80 || strings.ContainsAny(got, "\x00\n") {
		t.Fatal("unbounded attribution")
	}
	header := http.Header{"ChatGPT-Account-ID": {"one"}, "chatgpt-account-id": {"two"}}
	if validAccountHeaders(header) {
		t.Fatal("case-variant identity ambiguity accepted")
	}
	s := testService(t)
	rec := Record{ID: "bounded-labels", Provider: strings.Repeat("p", 100), App: strings.Repeat("a", 100)}
	if err := s.store.start(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Provider) != 80 || len(rec.App) != 80 {
		t.Fatal("start attribution unbounded")
	}
	rec.Provider = strings.Repeat("p", 200)
	if err := s.store.finish(rec); err != nil {
		t.Fatal(err)
	}
	records, _, err := s.store.records(nil, 5)
	if err != nil || len(records) != 1 || len(records[0].Provider) != 80 {
		t.Fatal("finish attribution unbounded", err)
	}
}
