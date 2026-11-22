package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type installerTestTransport func(*http.Request) (*http.Response, error)

func (f installerTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type installerObservedStore struct {
	Store
	writes chan error
}

func (s *installerObservedStore) Update(endpoint string, fn func(*Session) (*Session, error)) (*Session, error) {
	session, err := s.Store.Update(endpoint, fn)
	s.writes <- err
	return session, err
}

func TestForgetInstallationFencesDeliveredCallback(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-sign-in", true: "re-authorization"}[signedIn], func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			const endpoint = "https://remote.example/tools?account=one"
			const alias = "https://REMOTE.example:443/tools?account=one#fragment"
			c, other := ForInstaller("one"), ForInstaller("two")
			seed := func(c *Coordinator, endpoint string) {
				t.Helper()
				_, err := c.store.Update(endpoint, func(*Session) (*Session, error) {
					return &Session{Config: OAuth2Config{ClientID: "client"}, Token: &oauth2.Token{AccessToken: "existing"}}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if signedIn {
				seed(c, endpoint)
			}
			// Path and query are resource identity; installer identity is separate.
			for _, resource := range []string{"https://remote.example/other?account=one", "https://remote.example/tools?account=two"} {
				seed(c, resource)
			}
			seed(other, endpoint)
			host := NewCoordinator(NewFileStore(t.TempDir() + "/host.json"))
			seed(host, endpoint)
			observed := &installerObservedStore{Store: c.store, writes: make(chan error, 1)}
			c.store = observed

			tokenStarted, releaseToken := make(chan struct{}), make(chan struct{})
			defer close(releaseToken)
			client := &http.Client{Transport: installerTestTransport(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch r.URL.Path {
				case "/tools":
					return &http.Response{StatusCode: 401, Header: http.Header{"Www-Authenticate": {`Bearer resource_metadata="https://remote.example/.well-known/oauth-protected-resource/tools"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				case "/.well-known/oauth-protected-resource/tools":
					body = `{"resource":"` + endpoint + `","authorization_servers":["https://issuer.example"]}`
				case "/.well-known/oauth-authorization-server":
					body = `{"issuer":"https://issuer.example","authorization_endpoint":"https://issuer.example/authorize","token_endpoint":"https://issuer.example/token","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`
				case "/token":
					close(tokenStarted)
					// An exchange already in flight can deliver a token after disposal.
					// Deliberately ignore cancellation, with no real network involved.
					<-releaseToken
					body = `{"access_token":"late","token_type":"Bearer","expires_in":3600}`
				default:
					t.Errorf("unexpected OAuth request: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			flow, err := c.Start(t.Context(), alias, Options{ClientID: "client", HTTPClient: client}, "https://callback.example/oauth", true)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(flow.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := c.CompleteCallback(u.Query().Get("state"), "code", "", ""); !ok {
				t.Fatal("callback not delivered")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			select {
			case <-tokenStarted:
			case <-ctx.Done():
				t.Fatal("token exchange did not start")
			}
			if c.Cancel(endpoint, flow.ID) {
				t.Fatal("ordinary cancellation should still refuse a delivered callback")
			}
			if err := ForgetInstallation(ctx, "one", alias); err != nil {
				t.Fatal(err)
			}
			// Unblock the token response without closing twice on a failing assertion.
			releaseToken <- struct{}{}
			select {
			case err := <-observed.writes:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("late credential write = %v, want cancellation", err)
				}
			case <-ctx.Done():
				t.Fatal("late credential write was not attempted")
			}
			if status := c.Status(endpoint); status.State == AuthSignedIn {
				t.Fatal("late exchange resurrected the deleted account")
			}
			if flow, _ := c.Flow(flow.ID); flow.State != FlowCanceled {
				t.Fatalf("disposed flow state = %s", flow.State)
			}
			for _, resource := range []string{"https://remote.example/other?account=one", "https://remote.example/tools?account=two"} {
				if c.Status(resource).State != AuthSignedIn {
					t.Fatalf("disposal cleared another resource: %s", resource)
				}
			}
			if other.Status(endpoint).State != AuthSignedIn || host.Status(endpoint).State != AuthSignedIn {
				t.Fatal("disposal cleared another installer's or the host's account")
			}
		})
	}
}
