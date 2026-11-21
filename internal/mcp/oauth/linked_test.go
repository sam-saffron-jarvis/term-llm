package oauth

import (
	"context"
	"golang.org/x/oauth2"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestLinkedGrantIsolation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	own, other := ForInstaller("one"), ForInstaller("two")
	const endpoint = "https://remote.example/tools"
	_, err := own.store.Update(endpoint, func(*Session) (*Session, error) {
		return &Session{Endpoint: endpoint, Config: OAuth2Config{ClientID: "client"}, Token: &oauth2.Token{AccessToken: "test"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if own.Status(endpoint).State != AuthSignedIn || other.Status(endpoint).State == AuthSignedIn || own.store.Path() == DefaultCoordinator().store.Path() {
		t.Fatal("account isolation")
	}
	flow := Flow{ID: "flow", State: FlowPending, Endpoint: endpoint, ExpiresAt: time.Now().Add(time.Minute)}
	own.flows[flow.ID] = &flowRecord{flow: flow, callback: make(chan callbackResult, 1), callbackState: "state"}
	own.byState["state"] = flow.ID
	grant, revoke := context.WithCancel(context.Background())
	revoke()
	own.flows[flow.ID].grant = grant
	if _, ok := CompletePublicCallback("state", "code", "", ""); ok {
		t.Fatal("revoked flow callback accepted")
	}
	own.flows[flow.ID].grant = context.Background()
	if _, ok := other.Flow(flow.ID); ok {
		t.Fatal("other device could read flow")
	}
	if id, ok := CompletePublicCallback("state", "code", "", ""); !ok || id != flow.ID {
		t.Fatal("public callback missed delegated flow")
	}
	if _, ok := CompletePublicCallback("state", "code", "", ""); ok {
		t.Fatal("callback replay")
	}
	if res := <-own.flows[flow.ID].callback; res.code != "code" {
		t.Fatal("no delegated callback")
	}
}
func TestLinkedTransportRejectsNonPublicDestinations(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.ResponseHeaderTimeout = 2 * time.Minute
	tr := LinkedTransport(base)
	wrapped := tr.(linkedTransport).inner.(*http.Transport)
	if wrapped.ResponseHeaderTimeout != base.ResponseHeaderTimeout || wrapped.Proxy != nil {
		t.Fatal("linked policy must preserve client timeouts and disable proxies")
	}
	for _, raw := range []string{"http://public.example", "https://localhost/mcp", "https://203.0.113.1/mcp", "https://[2001:db8::1]/", "https://[::ffff:127.0.0.1]/", "https://169.254.169.254/", "https://100.100.100.200/", "https://user@target.example/", "https://remote.example:22/"} {
		req, _ := http.NewRequest(http.MethodGet, raw, nil)
		if _, err := tr.RoundTrip(req); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if publicAddress(netip.MustParseAddr("10.10.0.1")) || !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("dial destination policy")
	}
}
