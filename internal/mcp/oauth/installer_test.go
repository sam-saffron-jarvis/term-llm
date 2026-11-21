package oauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestRetireInstallerSealsAndDeletesOnlyThatDevice(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	const endpoint = "https://remote.example/tools"
	signIn := func(c *Coordinator) {
		t.Helper()
		if _, err := c.store.Update(endpoint, func(*Session) (*Session, error) {
			return &Session{Endpoint: endpoint, Config: OAuth2Config{ClientID: "client"}, Token: &oauth2.Token{AccessToken: "test"}}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RetireInstaller("never-signed-in"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, "term-llm", "mcp-linked")); !os.IsNotExist(err) {
		t.Fatalf("retiring an unused installer created state: %v", err)
	}

	old, other := ForInstaller("retire-one"), ForInstaller("retire-two")
	signIn(old)
	signIn(other)
	flowCtx, cancelFlow := context.WithCancel(context.Background())
	defer cancelFlow()
	flow := Flow{ID: "pending", State: FlowPending, Endpoint: endpoint, ExpiresAt: time.Now().Add(time.Minute)}
	old.flows[flow.ID] = &flowRecord{flow: flow, callback: make(chan callbackResult, 1), ready: make(chan struct{}), done: make(chan struct{}), cancel: cancelFlow, callbackState: "state"}
	old.byEndpoint[endpoint], old.byState["state"] = flow.ID, flow.ID

	if err := RetireInstaller("retire-one"); err != nil {
		t.Fatal(err)
	}
	if !old.Retired() || flowCtx.Err() == nil {
		t.Fatal("retired coordinator left a usable store or pending sign-in")
	}
	if _, err := os.Stat(old.store.Path()); !os.IsNotExist(err) {
		t.Fatalf("account store survived: %v", err)
	}
	if _, err := old.store.Update(endpoint, func(*Session) (*Session, error) { return &Session{}, nil }); !errors.Is(err, ErrInstallerRetired) {
		t.Fatalf("late write after retirement: %v", err)
	}
	if _, ok := CompletePublicCallback("state", "code", "", ""); ok {
		t.Fatal("retired sign-in accepted a callback")
	}
	if other.Status(endpoint).State != AuthSignedIn {
		t.Fatal("retirement touched another device's account")
	}
	fresh := ForInstaller("retire-one")
	if fresh == old || fresh.Retired() || fresh.Status(endpoint).State == AuthSignedIn {
		t.Fatal("re-granted device inherited the retired store")
	}
	signIn(fresh)
	if err := ForgetInstallation(context.Background(), "retire-one", endpoint); err != nil || fresh.Status(endpoint).State == AuthSignedIn {
		t.Fatalf("forget installation: %v", err)
	}
}
