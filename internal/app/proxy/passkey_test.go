package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/term-llm/term-llm/internal/passkeyauth"
)

const passkeyOrigin = "https://proxy.example.test"
const hostSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type passkeyBrowser struct {
	t       *testing.T
	s       *Service
	csrf    string
	cookies map[string]*http.Cookie
}

func passkeyFixture(t *testing.T) (*Service, Options) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(path, []byte(hostSecret), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{StateDir: filepath.Join(dir, "state"), PublicURL: passkeyOrigin, PasskeyBootstrapTokenFile: path}
	s, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, opts
}
func newPasskeyBrowser(t *testing.T, s *Service) *passkeyBrowser {
	b := &passkeyBrowser{t: t, s: s, cookies: map[string]*http.Cookie{}}
	b.status()
	return b
}
func (b *passkeyBrowser) request(method, path string, body io.Reader) *http.Request {
	b.t.Helper()
	r := httptest.NewRequest(method, "http://private-backend.invalid/_proxy/api/"+path, body)
	r.Host = "proxy.example.test"
	r.RemoteAddr = "127.0.0.1:5678"
	r.Header.Set("Origin", passkeyOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", b.csrf)
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	return r
}
func (b *passkeyBrowser) execute(r *http.Request, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	w := httptest.NewRecorder()
	b.s.ServeHTTP(w, r)
	if w.Code != want {
		b.t.Fatalf("%s %s: got %d, want %d: %s", r.Method, r.URL.Path, w.Code, want, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.CSRF != "" {
		b.csrf = out.CSRF
	}
	return w
}
func (b *passkeyBrowser) send(method, path string, value any, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.execute(b.request(method, path, bytes.NewReader(body)), want)
}
func (b *passkeyBrowser) status() *httptest.ResponseRecorder {
	return b.send("GET", "auth/status", nil, 200)
}
func (b *passkeyBrowser) begin(path string, value any) webauthn.SessionData {
	b.t.Helper()
	w := b.send("POST", "auth/"+path+"/begin", value, 200)
	var out struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.PublicKey.Challenge == "" {
		b.t.Fatalf("missing challenge: %s", w.Body)
	}
	return webauthn.SessionData{Challenge: out.PublicKey.Challenge}
}
func (b *passkeyBrowser) finish(path string, response *http.Request, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.execute(b.request("POST", "auth/"+path+"/finish", response.Body), want)
}
func bootstrapPasskey(t *testing.T, b *passkeyBrowser) (*webauthn.Credential, func(string, uint32)) {
	t.Helper()
	key, credential := localAssertionCredential(t)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
	data := b.begin("bootstrap", map[string]string{"name": "Primary key"})
	b.finish("bootstrap", registrationHTTPRequest(t, credential.PublicKey, credential.ID, data, passkeyOrigin, "proxy.example.test", true), 200)
	return &credential, func(action string, count uint32) {
		data := b.begin(action, struct{}{})
		b.finish(action, assertionHTTPRequest(t, key, credential, data, "webauthn.get", passkeyOrigin, "proxy.example.test", count, true, true), 200)
	}
}

func TestProxyPasskeyBootstrapLoginAndLogout(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	if s.opts.Auth != "passkey" || s.adminHash != "" {
		t.Fatal("default must be passkey only")
	}
	if !strings.Contains(b.status().Body.String(), `"setup_required":true`) {
		t.Fatal("expected first enrollment")
	}
	b.send("POST", "login", map[string]string{"token": hostSecret}, 401)
	b.send("GET", "state", nil, 401)
	credential, login := bootstrapPasskey(t, b)
	_ = credential
	c := b.cookies["tlp_admin"]
	if c == nil || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/_proxy/" || c.Domain != "" {
		t.Fatalf("unsafe session cookie: %+v", c)
	}
	if b.csrf == c.Value {
		t.Fatal("session token leaked as CSRF")
	}
	b.send("GET", "state", nil, 200)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 409)
	b.send("POST", "logout", struct{}{}, 200)
	if _, ok := b.cookies["tlp_admin"]; ok {
		t.Fatal("logout retained cookie")
	}
	b.send("GET", "state", nil, 401)
	b.status()
	login("login", 1)
	b.send("GET", "state", nil, 200)
	if strings.Contains(b.send("GET", "auth/credentials", nil, 200).Body.String(), "public_key") {
		t.Fatal("credential protocol details leaked")
	}
}

func TestProxyPasskeyHostOriginCSRFAndNoTokenBypass(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"wrong host", func(r *http.Request) {
			r.Host = "evil.example.test"
			r.Header.Set("X-Forwarded-Host", "proxy.example.test")
		}},
		{"wrong origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.test") }},
		{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }},
		{"missing CSRF", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }},
		{"wrong CSRF", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(map[string]string{"token": hostSecret})
			r := b.request("POST", "auth/bootstrap/verify", bytes.NewReader(data))
			test.mutate(r)
			b.execute(r, 403)
		})
	}
	r := b.request("GET", "state", nil)
	r.Header.Set("Authorization", "Bearer "+hostSecret)
	b.execute(r, 401)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 401)
}

func TestProxyPasskeyCredentialAndSessionRevocation(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	_, login := bootstrapPasskey(t, b)
	primary := s.passkey.store.Credentials()[0].RecordID
	b.send("DELETE", "auth/credentials", map[string]string{"id": primary}, 403)
	login("reauth", 1)
	b.send("DELETE", "auth/credentials", map[string]string{"id": primary}, 409)
	b.send("POST", "auth/sessions/revoke", struct{}{}, 403) // one-use even if mutation refused
	login("reauth", 2)
	key, secondary := localAssertionCredential(t)
	_ = key
	secondary.ID = []byte("second-key")
	data := b.begin("enroll", map[string]string{"name": "Spare"})
	b.finish("enroll", registrationHTTPRequest(t, secondary.PublicKey, secondary.ID, data, passkeyOrigin, "proxy.example.test", true), 200)
	b.send("POST", "auth/enroll/begin", map[string]string{"name": "No recent grant"}, 403)
	// A second browser for each credential: revocation is scoped to that key.
	second := newPasskeyBrowser(t, s)
	spareSession, err := s.passkey.sessions.Create(s.passkey.store.Credentials()[1].RecordID)
	if err != nil {
		t.Fatal(err)
	}
	second.cookies["tlp_admin"] = &http.Cookie{Name: "tlp_admin", Value: spareSession.Token}
	second.status()
	second.send("GET", "state", nil, 200)
	login("reauth", 3)
	b.send("DELETE", "auth/credentials", map[string]string{"id": spareSession.Info.CredentialRecordID}, 200)
	second.send("GET", "state", nil, 401)
	thirdSession, err := s.passkey.sessions.Create(primary)
	if err != nil {
		t.Fatal(err)
	}
	third := newPasskeyBrowser(t, s)
	third.cookies["tlp_admin"] = &http.Cookie{Name: "tlp_admin", Value: thirdSession.Token}
	third.status()
	login("reauth", 4)
	b.send("POST", "auth/sessions/revoke", struct{}{}, 200)
	third.send("GET", "state", nil, 401)
	b.send("GET", "state", nil, 200)
}

func TestProxyPasskeyRestartAndRecovery(t *testing.T) {
	s, opts := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	_, _ = bootstrapPasskey(t, b)
	oldCookie := b.cookies["tlp_admin"]
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Open a new adapter over persisted credentials; browser sessions are not persisted.
	opts.PasskeyBootstrapTokenFile = ""
	recovery := filepath.Join(filepath.Dir(opts.StateDir), "recovery")
	if err := os.WriteFile(recovery, []byte(hostSecret+"recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	opts.PasskeyRecoveryTokenFile = recovery
	restarted, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	b.s = restarted
	b.cookies["tlp_admin"] = oldCookie
	b.send("GET", "state", nil, 401)
	b.status()
	b.send("POST", "auth/recovery/verify", map[string]string{"token": hostSecret + "recovery"}, 200)
	_, replacement := localAssertionCredential(t)
	replacement.ID = []byte("recovered-key")
	data := b.begin("recovery", map[string]string{"name": "Replacement"})
	// An already admitted new session must also be invalidated on recovery.
	admitted, err := restarted.passkey.sessions.Create(restarted.passkey.store.Credentials()[0].RecordID)
	if err != nil {
		t.Fatal(err)
	}
	b.finish("recovery", registrationHTTPRequest(t, replacement.PublicKey, replacement.ID, data, passkeyOrigin, "proxy.example.test", true), 200)
	if restarted.passkey.sessions.Count() != 0 {
		t.Fatal("recovery retained sessions")
	}
	if _, err := restarted.passkey.sessions.Authenticate(admitted.Token); err == nil {
		t.Fatal("recovered session remained valid")
	}
	b.send("GET", "state", nil, 401)
	if restarted.passkey.store.CredentialCount() != 2 {
		t.Fatal("recovery must add key without deleting existing key")
	}
	b.send("POST", "auth/recovery/verify", map[string]string{"token": hostSecret + "recovery"}, 401)
}

func TestProxyPasskeyBoundedBodiesAndCeremonies(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": strings.Repeat("x", authBodyLimit)}, 400)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret, "extra": "no"}, 400)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
	_, credential := localAssertionCredential(t)
	data := b.begin("bootstrap", map[string]string{"name": "Primary"})
	b.finish("login", registrationHTTPRequest(t, credential.PublicKey, credential.ID, data, passkeyOrigin, "proxy.example.test", true), 401)
	b.finish("bootstrap", registrationHTTPRequest(t, credential.PublicKey, credential.ID, data, passkeyOrigin, "proxy.example.test", true), 200)
	b.finish("bootstrap", registrationHTTPRequest(t, credential.PublicKey, credential.ID, data, passkeyOrigin, "proxy.example.test", true), 409)
	// A recent-auth challenge cannot be redeemed after its session is revoked.
	reauth := b.begin("reauth", struct{}{})
	s.passkey.sessions = passkeyauth.NewSessions(nil, nil)
	b.finish("reauth", registrationHTTPRequest(t, credential.PublicKey, credential.ID, reauth, passkeyOrigin, "proxy.example.test", true), 401)
}
