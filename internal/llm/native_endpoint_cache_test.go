package llm

import (
	"testing"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestNativeGatewayCompletionsUseEndpointCache(t *testing.T) {
	for _, kind := range []config.ProviderType{config.ProviderTypeOpenRouter, config.ProviderTypeVenice, config.ProviderTypeZen} {
		t.Run(string(kind), func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			if err := cache.WriteModelInfoCache(string(kind), []cache.CachedModel{{ID: "public-only"}}); err != nil {
				t.Fatal(err)
			}
			pc := config.ProviderConfig{Type: kind, APIKey: "fake", BaseURL: "http://gateway.test/nested/v1/", Model: "configured"}
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{"corp": pc}}
			if got := GetProviderCompletions("corp:", false, cfg); !equalSlice(got, []string{"corp:configured"}) {
				t.Fatalf("empty gateway cache completions=%v", got)
			}
			RefreshEndpointCacheSync(pc, []core.ModelInfo{{ID: "gateway-only"}})
			other := pc
			other.BaseURL = "http://another.test/v1"
			RefreshEndpointCacheSync(other, []core.ModelInfo{{ID: "other-gateway"}})
			if got := GetProviderCompletions("corp:", false, cfg); !equalSlice(got, []string{"corp:configured", "corp:gateway-only"}) {
				t.Fatalf("gateway completions=%v", got)
			}
		})
	}
}
