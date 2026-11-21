package serve

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/mcp"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
	"github.com/term-llm/term-llm/internal/session"
	"golang.org/x/oauth2"
)

// A removed device installation leaves a stale name; the peer can deselect it.
// A configured host-only name still blocks the peer.
func TestLinkedMCPStaleSelectionCanBeDeselected(t *testing.T) {
	writeServeMCPConfig(t, map[string]mcp.ServerConfig{"private": {Command: "private-path"}})
	store := newServeMCPTestStore(t)
	srv, _ := newServeMCPHandlerTestServer(t, store)
	for id, selection := range map[string]string{"stale": "removed_install", "owned": "private,removed_install"} {
		if err := store.Create(t.Context(), &session.Session{ID: id, MCP: selection}); err != nil {
			t.Fatal(err)
		}
	}
	call := func(id, method, body string, want int) string {
		t.Helper()
		w := peerMCPRequest(srv.handleSessionByID, method, "/v1/sessions/"+id+"/mcp", body, "peer", false)
		if w.Code != want {
			t.Fatalf("%s %s -> %d: %s", method, id, w.Code, w.Body)
		}
		return w.Body.String()
	}
	if body := call("stale", "GET", "", 200); strings.Contains(body, "removed_install") {
		t.Fatalf("stale name shown as available: %s", body)
	}
	rt, _, err := srv.runtimeForRequest(t.Context(), "stale")
	if err != nil {
		t.Fatal(err)
	}
	if err := linkedMCPRunGuard(linkeddevices.DevicePrincipal{DeviceID: "peer", Scope: linkeddevices.ScopeWebV1}, rt, nil)(&core.Request{}); err != nil {
		t.Fatalf("stale, unstartable selection blocked a run: %v", err)
	}
	call("stale", "PATCH", `{"enabled":[]}`, 200)
	if sess, err := store.Get(t.Context(), "stale"); err != nil || sess.MCP != "" {
		t.Fatalf("stale selection kept: %+v, %v", sess, err)
	}
	if enabled := rt.mcpManager.EnabledServers(); len(enabled) != 0 {
		t.Fatalf("deselecting started servers: %v", enabled)
	}
	call("owned", "GET", "", 409)
	call("owned", "PATCH", `{"enabled":[]}`, 409)
}

func TestLinkedDeviceRemovalEndsOnlyItsOwnSignIn(t *testing.T) {
	const url = "https://remote.example/mcp"
	writeServeMCPConfig(t, map[string]mcp.ServerConfig{
		"first":  {Type: "http", URL: url, LinkedInstaller: "peer"},
		"second": {Type: "http", URL: url, LinkedInstaller: "peer"},
		"host":   {Type: "http", URL: url},
	})
	base, err := mcpoauth.DefaultStorePath()
	if err != nil {
		t.Fatal(err)
	}
	accounts := mcpoauth.NewFileStore(filepath.Join(filepath.Dir(base), "mcp-linked", fmt.Sprintf("%x.json", sha256.Sum256([]byte("peer")))))
	if _, err := accounts.Update(url, func(*mcpoauth.Session) (*mcpoauth.Session, error) {
		return &mcpoauth.Session{Endpoint: url, Config: mcpoauth.OAuth2Config{ClientID: "client"}, Token: &oauth2.Token{AccessToken: "test"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	srv, _ := newServeMCPHandlerTestServer(t, newServeMCPTestStore(t))
	remove := func(name string, want int) {
		t.Helper()
		if w := peerMCPRequest(srv.handleMCPServerByName, "DELETE", "/v1/mcp/servers/"+name, "", "peer", true); w.Code != want {
			t.Fatalf("remove %s -> %d: %s", name, w.Code, w.Body)
		}
	}
	signedIn := func() bool { _, err := accounts.Load(url); return err == nil }
	remove("host", 403)
	remove("first", 200)
	if !signedIn() {
		t.Fatal("removing one installation ended a sign-in another still uses")
	}
	remove("second", 200)
	if signedIn() {
		t.Fatal("device removal kept its sign-in")
	}
}
