package usage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAncillaryUsageConfiguredRoots(t *testing.T) {
	paths := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path]++
		switch r.URL.Path {
		case "/proxy/catalog/api.json":
			fmt.Fprint(w, modelsDevFixture)
		case "/proxy/pricing/BerriAI/litellm/main/model_prices_and_context_window.json":
			fmt.Fprint(w, `{"test":{"input_cost_per_token":0.1,"output_cost_per_token":0.2}}`)
		case "/proxy/account/key":
			fmt.Fprint(w, `{"data":{"usage":1}}`)
		case "/proxy/account/credits":
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":1}}`)
		default:
			t.Errorf("URL=%s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("MODELS_DEV_BASE_URL", server.URL+"/proxy/catalog/")
	t.Setenv("LITELLM_PRICING_BASE_URL", server.URL+"/proxy/pricing/")
	t.Setenv("OPENROUTER_USAGE_BASE_URL", server.URL+"/proxy/account/")
	if _, err := fetchModelsDevPricing(context.Background(), server.Client()); err != nil {
		t.Fatal(err)
	}
	p := NewPricingFetcher()
	p.cacheDir = t.TempDir()
	if err := p.fetch(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := FetchOpenRouterUsage(context.Background(), OpenRouterOptions{APIKey: func() (string, error) { return "fake", nil }, Since: now.Add(-time.Hour), Until: now, Now: now}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("paths=%v", paths)
	}
}
