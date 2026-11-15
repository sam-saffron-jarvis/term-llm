package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/term-llm/term-llm/internal/passkeyauth"
)

// Drive the real public endpoint with abandoned login challenges, including a
// shared TLS-terminator peer and enough distinct peers to fill the global limit.
// Protected reauthentication/enrollment/recovery must have independent capacity.
func TestProxyPasskeyLoginFloodCannotPinProtectedCeremonies(t *testing.T) {
	s, _ := passkeyFixture(t)
	admin := newPasskeyBrowser(t, s)
	_, reauth := bootstrapPasskey(t, admin)
	flood := newPasskeyBrowser(t, s)
	body := []byte("{}")
	floodLogin := func(peer string) {
		t.Helper()
		browser := flood
		if peer != "127.0.0.1:5678" {
			// Distinct browser principals fill the global pool even behind one
			// reverse-proxy address. The attacker cookie itself stays bounded.
			browser = newPasskeyBrowser(t, s)
		}
		r := browser.request(http.MethodPost, "auth/login/begin", bytes.NewReader(body))
		r.RemoteAddr = peer
		browser.execute(r, 200)
	}
	for i := 0; i < passkeyauth.MaxCeremonies; i++ {
		peer := "127.0.0.1:5678"
		if i >= passkeyauth.MaxCeremoniesPerPeer {
			peer = fmt.Sprintf("192.0.2.%d:1234", i/passkeyauth.MaxCeremoniesPerPeer)
		}
		floodLogin(peer)
	}
	// Continued same-peer begins replace the oldest login; never a five-minute 429.
	for i := 0; i < passkeyauth.MaxCeremoniesPerPeer+2; i++ {
		floodLogin("127.0.0.1:5678")
	}
	reauth("reauth", 1)
	key, spare := localAssertionCredential(t)
	spare.ID = []byte("spare-after-flood")
	enrollment := admin.begin("enroll", map[string]string{"name": "Spare"})
	// An in-flight protected challenge must survive more public global pressure.
	for i := 0; i < passkeyauth.MaxCeremonies; i++ {
		floodLogin(fmt.Sprintf("198.51.100.%d:1234", i))
	}
	admin.finish("enroll", registrationHTTPRequest(t, spare.PublicKey, spare.ID, enrollment, passkeyOrigin, "proxy.example.test", true), 200)
	admin.send("GET", "state", nil, 200)
	// Fresh public login still completes under full capacity and stays one-use.
	login := flood.begin("login", struct{}{})
	makeAssertion := func() *http.Request {
		return assertionHTTPRequest(t, key, spare, login, "webauthn.get", passkeyOrigin, "proxy.example.test", 1, true, true)
	}
	flood.finish("login", makeAssertion(), 200)
	flood.finish("login", makeAssertion(), 401)
	// Recovery is grant-protected, including when all public slots are occupied.
	var err error
	s.passkey.recovery, err = passkeyauth.NewGrants(passkeyauth.GrantRecovery, []byte(hostSecret), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recovery := newPasskeyBrowser(t, s)
	recovery.send("POST", "auth/recovery/verify", map[string]string{"token": hostSecret}, 200)
	data := recovery.begin("recovery", map[string]string{"name": "Recovered"})
	_, replacement := localAssertionCredential(t)
	replacement.ID = []byte("recovery-after-flood")
	for i := 0; i < passkeyauth.MaxCeremonies; i++ {
		floodLogin(fmt.Sprintf("203.0.113.%d:1234", i))
	}
	recovery.finish("recovery", registrationHTTPRequest(t, replacement.PublicKey, replacement.ID, data, passkeyOrigin, "proxy.example.test", true), 200)
	admin.send("GET", "state", nil, 401)
	flood.send("GET", "state", nil, 401)
	recovery.send("GET", "state", nil, 401)
}

func TestProxyPasskeyBootstrapHasSeparateCapacityFromPublicLogin(t *testing.T) {
	s, _ := passkeyFixture(t)
	// Before first enrollment public login correctly refuses to begin. Prefill its
	// registry directly to prove bootstrap never depends on even its global bound.
	for i := 0; i < passkeyauth.MaxCeremonies; i++ {
		if _, err := s.passkey.logins.Create(passkeyauth.CeremonyLogin, fmt.Sprintf("peer-%d", i), "", "", "", nil, webauthn.SessionData{}); err != nil {
			t.Fatal(err)
		}
	}
	browser := newPasskeyBrowser(t, s)
	_, _ = bootstrapPasskey(t, browser)
	browser.send("GET", "state", nil, 200)
}

func TestProxyPasskeyPublicOldestEvictionInvalidatesOnlyEvictedLogin(t *testing.T) {
	s, _ := passkeyFixture(t)
	admin := newPasskeyBrowser(t, s)
	_, _ = bootstrapPasskey(t, admin)
	browser := newPasskeyBrowser(t, s)
	first := browser.begin("login", struct{}{})
	firstCookie := browser.cookies["tlp_ceremony"]
	for i := 0; i < passkeyauth.MaxCeremoniesPerPeer; i++ {
		browser.begin("login", struct{}{})
	}
	browser.cookies["tlp_ceremony"] = firstCookie
	_, raw := localAssertionCredential(t)
	response := registrationHTTPRequest(t, raw.PublicKey, raw.ID, first, passkeyOrigin, "proxy.example.test", true)
	browser.finish("login", response, 401)
	// The original authenticated session and state remain valid, not just its cookie.
	state := admin.send("GET", "state", nil, 200)
	var payload map[string]any
	if err := json.Unmarshal(state.Body.Bytes(), &payload); err != nil || payload["auth"] != "passkey" {
		t.Fatal("protected state affected by eviction")
	}
}

func TestProxyPasskeySharedPeerFloodDoesNotEvictAnotherBrowserPrompt(t *testing.T) {
	s, _ := passkeyFixture(t)
	admin := newPasskeyBrowser(t, s)
	key, credential := localAssertionCredential(t)
	admin.send("POST", "auth/bootstrap/verify", map[string]string{"token": hostSecret}, 200)
	setup := admin.begin("bootstrap", map[string]string{"name": "Primary"})
	admin.finish("bootstrap", registrationHTTPRequest(t, credential.PublicKey, credential.ID, setup, passkeyOrigin, "proxy.example.test", true), 200)
	victim := newPasskeyBrowser(t, s)
	challenge := victim.begin("login", struct{}{})
	attacker := newPasskeyBrowser(t, s)
	// All these requests have the same default RemoteAddr, as behind Caddy.
	// Keep the victim's actual prompt in flight while another browser floods.
	for i := 0; i < passkeyauth.MaxCeremoniesPerPeer+4; i++ {
		attacker.begin("login", struct{}{})
	}
	response := assertionHTTPRequest(t, key, credential, challenge, "webauthn.get", passkeyOrigin, "proxy.example.test", 1, true, true)
	victim.finish("login", response, 200)
	victim.send("GET", "state", nil, 200)
}
