package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/providers/opencompat"
)

func TestServeOpenRouterGatewaySkipsPublicCatalog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.WriteModelInfoCache("openrouter", []cache.CachedModel{{ID: "public-only"}}); err != nil {
		t.Fatal(err)
	}
	called := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/nested/v1/models" {
			t.Errorf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"gateway-only"}]}`)
	}))
	defer api.Close()
	pc := config.ProviderConfig{Type: config.ProviderTypeOpenRouter, BaseURL: api.URL + "/nested/v1", ResolvedAPIKey: "fake", Model: "gateway-only"}
	p := opencompat.NewOpenRouterProvider("fake", "gateway-only", "", "")
	p.SetBaseURL(pc.BaseURL)
	srv := &serveServer{cfgRef: &config.Config{DefaultProvider: "openrouter", Providers: map[string]config.ProviderConfig{"openrouter": pc}}, modelsProviders: map[string]core.Provider{"openrouter": p}}
	rr := httptest.NewRecorder()
	srv.handleModels(rr, httptest.NewRequest(http.MethodGet, "/v1/models?provider=openrouter", nil))
	if rr.Code != 200 || !called || !strings.Contains(rr.Body.String(), "gateway-only") || strings.Contains(rr.Body.String(), "public-only") {
		t.Fatalf("code=%d called=%v body=%s", rr.Code, called, rr.Body)
	}
}
