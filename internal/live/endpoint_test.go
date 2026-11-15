package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/credentials"
)

func TestLiveFactoryResolvesEndpointsAtStart(t *testing.T) {
	for _, kind := range []string{"openai", "chatgpt", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if kind == "chatgpt" {
				if err := credentials.SaveChatGPTCredentials(&credentials.ChatGPTCredentials{AccessToken: "fake", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				want := map[string]string{"openai": "/nested/v1/realtime/calls", "chatgpt": "/nested/v1/realtime/calls", "gemini": "/nested/v1/live"}[kind]
				if r.URL.Path != want {
					t.Errorf("path=%s", r.URL.Path)
				}
				if kind == "gemini" && (r.URL.Query().Get("custom") != "x&y" || r.URL.Query().Get("key") != "fake") {
					t.Errorf("query=%q", r.URL.RawQuery)
				}
				http.Error(w, "test", 400)
			}))
			defer server.Close()
			endpointFile := filepath.Join(t.TempDir(), "endpoint")
			raw := "file://" + endpointFile
			cfg := config.LiveConfig{Provider: kind}
			switch kind {
			case "openai":
				cfg.OpenAI.APIKey = "fake"
				cfg.OpenAI.Model = "gpt-realtime-2.1"
				cfg.OpenAI.BaseURL = raw
			case "chatgpt":
				cfg.ChatGPT.CallBaseURL = raw
				cfg.ChatGPT.SidebandBaseURL = server.URL + "/side"
			case "gemini":
				cfg.Gemini.APIKey = "fake"
				cfg.Gemini.BaseURL = raw
			}
			provider, err := NewProviderWithClient(cfg, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			endpoint := server.URL + "/nested/v1/"
			if kind == "gemini" {
				endpoint = server.URL + "/nested/v1/live?custom=x%26y"
			}
			if err := os.WriteFile(endpointFile, []byte(endpoint), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err = provider.Start(ctx, "v=0\r\na=offer\r\n", SessionOptions{}); err == nil {
				t.Fatal("expected test server HTTP failure")
			}
			if !called {
				t.Fatal(fmt.Sprintf("%s did not request configured endpoint", kind))
			}
		})
	}
}
