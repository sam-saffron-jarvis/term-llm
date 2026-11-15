package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryConfiguredRoot(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/proxy/nested/v0/servers" || r.URL.Query().Get("search") != "x &?#" {
			t.Errorf("URL=%s", r.URL)
		}
		fmt.Fprint(w, `{"servers":[],"metadata":{}}`)
	}))
	defer server.Close()
	t.Setenv("MCP_REGISTRY_BASE_URL", server.URL+"/proxy/nested/")
	if _, err := NewRegistryClient().Search(context.Background(), SearchOptions{Query: "x &?#"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("missing request")
	}
}
