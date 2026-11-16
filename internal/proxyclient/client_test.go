package proxyclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLookupUsesSchemeAuthorityAndPathBoundary(t *testing.T) {
	root := "https://binding-boundary.example.test/team/chatgpt"
	if err := Register([]Binding{{Root: root, Kind: "chatgpt", Token: strings.Repeat("t", 32)}}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{root, root + "/responses", "wss://binding-boundary.example.test/team/chatgpt/responses"} {
		if _, ok := Lookup(endpoint); !ok {
			t.Errorf("not bound: %s", endpoint)
		}
	}
	for _, endpoint := range []string{root + "-evil/responses", "https://binding-boundary.example.test.evil/team/chatgpt/responses", "http://binding-boundary.example.test/team/chatgpt/responses", "https://binding-boundary.example.test:444/team/chatgpt/responses", "https://binding-boundary.example.test/team/chatgpt%2fresponses"} {
		if _, ok := Lookup(endpoint); ok {
			t.Errorf("overbroad binding: %s", endpoint)
		}
	}
}
func TestScopedRedirectNeverReachesAnotherHostForCustomKey(t *testing.T) {
	seen := false
	escape := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		t.Error("redirect was followed with scoped custom key")
	}))
	defer escape.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "scoped-not-upstream" {
			t.Error("missing custom key")
		}
		http.Redirect(w, r, escape.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	if err := Register([]Binding{{Root: origin.URL + "/root", Token: "scoped-not-upstream"}}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", origin.URL+"/root/messages", strings.NewReader("synthetic"))
	req.Header.Set("x-api-key", "scoped-not-upstream")
	resp, err := Do(origin.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 307 || seen {
		t.Fatal("scoped redirect escaped")
	}
}
func TestNoVendorAuthStillAuthenticatesToExplicitProxyRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer machine-token" {
			t.Errorf("no caller token")
		}
		io.WriteString(w, "{}")
	}))
	defer server.Close()
	if err := Register([]Binding{{Root: server.URL + "/ollama", Token: "machine-token"}}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", server.URL+"/ollama/api/tags", nil)
	resp, err := Do(server.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if req.Header.Get("Authorization") != "" {
		t.Error("caller request mutated")
	}
}
func TestRelatedRequiresSamePrincipalAndCredentialKind(t *testing.T) {
	root := "https://related.example.test/mount/"
	if err := Register([]Binding{{Root: root + "chatgpt", Kind: "chatgpt", Token: "first"}, {Root: root + "chatgpt-backend", Kind: "chatgpt", Token: "first"}, {Root: root + "other", Kind: "chatgpt", Token: "other"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Related(root+"chatgpt/responses", "chatgpt-backend"); !ok {
		t.Fatal("no explicit sibling")
	}
	if _, ok := Related(root+"chatgpt", "other"); ok {
		t.Fatal("different principal escaped")
	}
	if _, ok := Related(root+"chatgpt", "unregistered"); ok {
		t.Fatal("constructed unregistered target")
	}
}
