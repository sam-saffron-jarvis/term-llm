package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm"
)

func TestNativeGatewayCompletionRefreshUsesConfiguredRoot(t *testing.T) {
	old := refreshZenModelsForCompletion
	refreshZenModelsForCompletion = func(context.Context, string, string) error {
		t.Error("public Zen refresh invoked for gateway")
		return nil
	}
	defer func() { refreshZenModelsForCompletion = old }()
	for _, kind := range []config.ProviderType{config.ProviderTypeOpenRouter, config.ProviderTypeVenice, config.ProviderTypeZen} {
		t.Run(string(kind), func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			called := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.URL.Path != "/nested/v1/models" {
					t.Errorf("URL=%s", r.URL)
				}
				fmt.Fprint(w, `{"data":[{"id":"gateway-only"}]}`)
			}))
			defer server.Close()
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{"corp": {Type: kind, BaseURL: server.URL + "/nested/v1/", APIKey: "fake", Model: "configured"}}}
			refreshProviderCompletionCaches("corp:", cfg)
			if called != 1 {
				t.Fatalf("requests=%d", called)
			}
			if got := llm.GetProviderCompletions("corp:", false, cfg); !slices.Equal(got, []string{"corp:configured", "corp:gateway-only"}) {
				t.Fatalf("completions=%v", got)
			}
		})
	}
}

func TestNativeGatewayCompletionSkipsDeferredRoots(t *testing.T) {
	old := refreshEndpointModelsForCompletion
	refreshEndpointModelsForCompletion = func(context.Context, config.ProviderConfig) error {
		t.Error("deferred gateway reference resolved by completion")
		return nil
	}
	defer func() { refreshEndpointModelsForCompletion = old }()
	for _, kind := range []config.ProviderType{config.ProviderTypeOpenRouter, config.ProviderTypeVenice, config.ProviderTypeZen} {
		cfg := &config.Config{Providers: map[string]config.ProviderConfig{"corp": {Type: kind, BaseURL: "file:///not-available-until-inference", APIKey: "fake"}}}
		refreshEndpointCompletionCache("corp:", cfg)
	}
}
