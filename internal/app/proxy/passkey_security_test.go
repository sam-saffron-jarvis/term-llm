package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/passkeyauth"
)

func TestProxyPasskeyStartupPolicy(t *testing.T) {
	_, opts := passkeyFixture(t)
	for _, test := range []struct {
		name   string
		change func(*Options)
	}{
		{"admin token bypass", func(o *Options) { o.AdminTokenFile = o.PasskeyBootstrapTokenFile }},
		{"missing origin", func(o *Options) { o.PublicURL = "" }},
		{"insecure remote", func(o *Options) { o.PublicURL = "http://proxy.example.test" }},
		{"IP origin", func(o *Options) { o.PublicURL = "https://127.0.0.1" }},
		{"mount path", func(o *Options) { o.PublicURL += "/custom/" }},
		{"query", func(o *Options) { o.PublicURL += "?query" }},
		{"fragment", func(o *Options) { o.PublicURL += "#fragment" }},
		{"no bootstrap", func(o *Options) { o.PasskeyBootstrapTokenFile = "" }},
		{"recovery before enrollment", func(o *Options) { o.PasskeyRecoveryTokenFile = o.PasskeyBootstrapTokenFile }},
		{"invalid auth mode", func(o *Options) { o.Auth = "automatic" }},
		{"token mixed passkeys", func(o *Options) { o.Auth = "token"; o.AdminTokenFile = o.PasskeyBootstrapTokenFile }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := opts
			candidate.StateDir = filepath.Join(t.TempDir(), "state")
			test.change(&candidate)
			s, err := Open(candidate)
			if err == nil {
				s.Close()
				t.Fatal("unsafe options accepted")
			}
		})
	}
	t.Run("localhost HTTP development", func(t *testing.T) {
		candidate := opts
		candidate.StateDir = filepath.Join(t.TempDir(), "state")
		candidate.PublicURL = "http://localhost:8082"
		s, err := Open(candidate)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.passkey.endpoint.Secure || s.opts.PublicURL != candidate.PublicURL {
			t.Fatal("localhost endpoint policy changed")
		}
	})
}

func TestProxyPasskeyPrivateFilesAndExclusiveState(t *testing.T) {
	s, opts := passkeyFixture(t)
	if duplicate, err := Open(opts); err == nil {
		duplicate.Close()
		t.Fatal("two live passkey adapters share state")
	}
	info, err := os.Stat(s.passkey.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("credential state not private")
	}
	for _, kind := range []string{"symlink", "public", "large", "short"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "host-secret")
			body := hostSecret
			if kind == "large" {
				body = strings.Repeat("x", 4097)
			}
			if kind == "short" {
				body = "short"
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "public" {
				if runtime.GOOS == "windows" {
					t.Skip("Windows privacy policy uses ACLs")
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				link := path + "-link"
				if err := os.Symlink(path, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				path = link
			}
			candidate := opts
			candidate.StateDir = filepath.Join(dir, "state")
			candidate.PasskeyBootstrapTokenFile = path
			if opened, err := Open(candidate); err == nil {
				opened.Close()
				t.Fatal("unsafe host secret accepted")
			}
		})
	}
}

func TestProxyPasskeyRejectsInvalidAssertions(t *testing.T) {
	for _, kind := range []string{"origin", "rp", "UV", "signature", "challenge", "replay"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := passkeyFixture(t)
			b := newPasskeyBrowser(t, s)
			_, login := bootstrapPasskey(t, b)
			_ = login
			// Use a fresh credential, so the test owns its signing key.
			key, raw := localAssertionCredential(t)
			raw.ID = []byte("assertion-key")
			if _, err := s.passkey.store.AddCredential(raw, "Assertion test"); err != nil {
				t.Fatal(err)
			}
			b.send("POST", "logout", struct{}{}, 200)
			b.status()
			data := b.begin("login", struct{}{})
			origin, rp := passkeyOrigin, "proxy.example.test"
			uv, signature := true, true
			switch kind {
			case "origin":
				origin = "https://evil.example.test"
			case "rp":
				rp = "evil.example.test"
			case "UV":
				uv = false
			case "signature":
				signature = false
			case "challenge":
				data.Challenge = "bm90LXRoZS1zZXJ2ZXItY2hhbGxlbmdl"
			}
			makeResponse := func() *http.Request {
				return assertionHTTPRequest(t, key, raw, data, "webauthn.get", origin, rp, 1, uv, signature)
			}
			want := 401
			if kind == "replay" {
				want = 200
			}
			b.finish("login", makeResponse(), want)
			b.finish("login", makeResponse(), 401)
			if want == 401 {
				b.send("GET", "state", nil, 401)
			}
		})
	}
}

func TestProxyPasskeyFileSecretsCannotReadAuthMaterial(t *testing.T) {
	s, opts := passkeyFixture(t)
	dir := t.TempDir()
	upstream := filepath.Join(dir, "upstream")
	if err := os.WriteFile(upstream, []byte("upstream-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{opts.PasskeyBootstrapTokenFile, s.passkey.store.Path(), filepath.Join(opts.StateDir, "admin-auth", "auth.lock")}
	for _, target := range paths {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Link(target, alias); err == nil {
			paths = append(paths, alias)
		}
	}
	for _, path := range paths {
		c := Config{Secrets: map[string]SecretRef{"forbidden": {Origin: "https://upstream.example.test", File: path}}}
		if _, err := s.resolveSecrets(c, nil, true); err == nil {
			t.Fatalf("auth material accepted as upstream: %s", path)
		}
	}
	c := Config{Secrets: map[string]SecretRef{"valid": {Origin: "https://upstream.example.test", File: upstream}}}
	if materials, err := s.resolveSecrets(c, nil, true); err != nil || materials["valid"].Value != "upstream-secret" {
		t.Fatalf("ordinary file secrets broken without admin-token file: %v", err)
	}
}

func TestProxyPasskeyReauthCeremonyBoundToOriginalSession(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	_, _ = bootstrapPasskey(t, b)
	data := b.begin("reauth", struct{}{})
	raw := s.passkey.store.Credentials()[0]
	issued, err := s.passkey.sessions.Create(raw.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	b.cookies["tlp_admin"] = &http.Cookie{Name: "tlp_admin", Value: issued.Token}
	b.status()
	// Even malformed credentials must fail on binding, before protocol parsing.
	b.finish("reauth", registrationHTTPRequest(t, raw.WebAuthn.PublicKey, raw.WebAuthn.ID, data, passkeyOrigin, "proxy.example.test", true), 401)
	if s.passkey.sessions.HasRecentAuth(passkeyauth.Principal{SessionID: issued.Info.ID, CredentialRecordID: raw.RecordID}) {
		t.Fatal("recent auth crossed sessions")
	}
}

func TestProxyPasskeyValidHostSecretSurvivesSharedPeerThrottle(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	for i := 0; i < 8; i++ {
		b.send("POST", "auth/bootstrap/verify", map[string]string{"token": "wrong"}, 401)
	}
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": "wrong"}, 429)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
}

func TestProxyPasskeyInterruptedBootstrapGrantSurvivesStatus(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
	if s.passkey.bootstrap.Enabled() {
		t.Fatal("host secret was not consumed at exchange")
	}
	status := b.status().Body.String()
	if !strings.Contains(status, `"bootstrap_available":true`) || !strings.Contains(status, `"bootstrap_verified":true`) {
		t.Fatalf("interrupted enrollment cannot resume: %s", status)
	}
	// Browser cancellation leaves the grant usable for a new challenge, not a second root exchange.
	b.begin("bootstrap", map[string]string{"name": "Cancelled"})
	b.begin("bootstrap", map[string]string{"name": "Retried"})
}

func TestProxyPasskeyMachineCallersIndependentOfBrowserSessions(t *testing.T) {
	s, _ := passkeyFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Upstream-Key") != "Real upstream" || r.Header.Get("Cookie") != "" {
			t.Error("forwarding credential isolation changed")
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	configure(t, s, Config{Routes: []Route{route("service", "/service/", upstream.URL+"/base/", "X-Caller", "key")}}, map[string]material{"key": boundMaterial(t, upstream.URL, "upstream")})
	token := caller(t, s, "service")
	forward := func() {
		t.Helper()
		if w := call(s, "POST", "/service/action", "X-Caller", token, nil); w.Code != 204 {
			t.Fatalf("machine token failed: %d %s", w.Code, w.Body)
		}
	}
	// Machine callers work before browser enrollment and after browser logout.
	forward()
	b := newPasskeyBrowser(t, s)
	_, _ = bootstrapPasskey(t, b)
	forward()
	b.send("POST", "logout", struct{}{}, 200)
	forward()
	// Neither a bootstrap secret nor a browser session can serve as a caller token.
	if w := call(s, "POST", "/service/action", "X-Caller", hostSecret, nil); w.Code != 401 {
		t.Fatalf("host secret forwarded: %d", w.Code)
	}
}

func TestProxyPasskeySharedLifetimesEnforcedAtHTTPBoundary(t *testing.T) {
	t.Run("idle session", func(t *testing.T) {
		s, _ := passkeyFixture(t)
		now := time.Now()
		s.passkey.sessions = passkeyauth.NewSessions(func() time.Time { return now }, nil)
		b := newPasskeyBrowser(t, s)
		_, _ = bootstrapPasskey(t, b)
		now = now.Add(passkeyauth.SessionIdleLifetime + time.Second)
		b.send("GET", "state", nil, 401)
	})
	t.Run("absolute session", func(t *testing.T) {
		s, _ := passkeyFixture(t)
		now := time.Now()
		start := now
		s.passkey.sessions = passkeyauth.NewSessions(func() time.Time { return now }, nil)
		b := newPasskeyBrowser(t, s)
		_, _ = bootstrapPasskey(t, b)
		for i := 0; i < 15; i++ {
			now = now.Add(11 * time.Hour)
			b.send("GET", "state", nil, 200)
		}
		now = start.Add(passkeyauth.SessionAbsoluteLifetime + time.Second)
		b.send("GET", "state", nil, 401)
	})
	t.Run("host grant", func(t *testing.T) {
		s, _ := passkeyFixture(t)
		now := time.Now()
		var err error
		s.passkey.bootstrap, err = passkeyauth.NewGrants(passkeyauth.GrantBootstrap, []byte(hostSecret), func() time.Time { return now }, nil)
		if err != nil {
			t.Fatal(err)
		}
		b := newPasskeyBrowser(t, s)
		now = now.Add(passkeyauth.HostGrantLifetime + time.Second)
		if strings.Contains(b.status().Body.String(), `"bootstrap_available":true`) {
			t.Fatal("expired secret still offered")
		}
		b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 401)
	})
	t.Run("ceremony", func(t *testing.T) {
		s, _ := passkeyFixture(t)
		now := time.Now()
		s.passkey.ceremonies = passkeyauth.NewCeremonies(func() time.Time { return now }, nil)
		b := newPasskeyBrowser(t, s)
		b.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
		_, raw := localAssertionCredential(t)
		data := b.begin("bootstrap", map[string]string{"name": "Expired"})
		now = now.Add(passkeyauth.CeremonyLifetime + time.Second)
		b.finish("bootstrap", registrationHTTPRequest(t, raw.PublicKey, raw.ID, data, passkeyOrigin, "proxy.example.test", true), 401)
	})
}

func TestProxyPasskeySelfRevocationClearsCookie(t *testing.T) {
	s, _ := passkeyFixture(t)
	browser := newPasskeyBrowser(t, s)
	_, reauth := bootstrapPasskey(t, browser)
	primary := s.passkey.store.Credentials()[0].RecordID
	_, spare := localAssertionCredential(t)
	spare.ID = []byte("self-revoke-spare")
	if _, err := s.passkey.store.AddCredential(spare, "Spare"); err != nil {
		t.Fatal(err)
	}
	reauth("reauth", 1)
	browser.send("DELETE", "auth/credentials", map[string]string{"id": primary}, 200)
	if _, ok := browser.cookies["tlp_admin"]; ok {
		t.Fatal("self revocation retained cookie")
	}
	browser.send("GET", "state", nil, 401)
}
