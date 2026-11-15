package responsesproviders

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredOpenCodeCatalogRoot(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/catalog/nested/api.json" || r.Header.Get("Authorization") != "" {
			t.Errorf("request=%s headers=%v", r.URL, r.Header)
		}
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	t.Setenv("OPENCODE_CATALOG_BASE_URL", server.URL+"/catalog/nested/")
	got, err := configuredOpenCodeCatalogURL(opencodeGoCatalogURL)
	if err != nil || got != server.URL+"/catalog/nested/api.json" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	fetchZenReasoningEfforts(context.Background())
	if !called {
		t.Fatal("missing catalog request")
	}
	if got, err := configuredOpenCodeCatalogURL(""); err != nil || got != "" {
		t.Fatalf("custom gateway catalog changed: %q %v", got, err)
	}
}
