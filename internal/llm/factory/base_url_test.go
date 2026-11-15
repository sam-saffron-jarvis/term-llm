package factory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/credentials"
	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestNativeFactoryBaseURLInference(t *testing.T) {
	for _, tc := range []struct {
		kind config.ProviderType
		path string
	}{
		{config.ProviderTypeGemini, "/nested/v1/models/test-model:streamGenerateContent"},
		{config.ProviderTypeXAI, "/nested/v1/chat/completions"},
		{config.ProviderTypeOpenRouter, "/nested/v1/chat/completions"},
		{config.ProviderTypeVenice, "/nested/v1/chat/completions"},
		{config.ProviderTypeNearAI, "/nested/v1/chat/completions"},
		{config.ProviderTypeSambaNova, "/nested/v1/chat/completions"},
		{config.ProviderTypeZen, "/nested/v1/chat/completions"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			seen := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.URL.Path
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			cfg := &config.ProviderConfig{Type: tc.kind, BaseURL: server.URL + "/nested/v1///", Model: "test-model", ResolvedAPIKey: "test-key"}
			provider, err := Create(string(tc.kind), cfg, Options{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := provider.Stream(ctx, core.Request{Messages: []core.Message{core.UserText("hello")}})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = stream.Recv()
			stream.Close()
			select {
			case got := <-seen:
				if got != tc.path {
					t.Fatalf("path = %q, want %q", got, tc.path)
				}
			case <-ctx.Done():
				t.Fatal("no request reached configured endpoint")
			}
		})
	}
}

func TestBedrockFactoryBaseURLInference(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "test-token")
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := &config.ProviderConfig{Type: config.ProviderTypeBedrock, BaseURL: server.URL + "/nested/", Region: "us-east-1", Model: "test-model"}
	provider, err := Create("bedrock", cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := provider.Stream(ctx, core.Request{Messages: []core.Message{core.UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	stream.Close()
	select {
	case got := <-seen:
		if got != "/nested/model/test-model/invoke-with-response-stream" {
			t.Fatalf("path = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("no request reached configured endpoint")
	}
}

func TestOAuthFactoryModelDiscoveryUsesConfiguredRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	expires := time.Now().Add(time.Hour).Unix()
	if err := credentials.SaveChatGPTCredentials(&credentials.ChatGPTCredentials{AccessToken: "test", AccountID: "account", ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SaveGrokCredentials(&credentials.GrokCredentials{AccessToken: "test", RefreshToken: "refresh", AccountID: "account", ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []config.ProviderType{config.ProviderTypeChatGPT, config.ProviderTypeGrok} {
		t.Run(string(kind), func(t *testing.T) {
			seen := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.URL.Path
				if kind == config.ProviderTypeGrok {
					io.WriteString(w, `{"data":[{"model":"grok-4.6","api_backend":"responses"}]}`)
				} else {
					io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT"}]}`)
				}
			}))
			defer server.Close()
			provider, err := Create(string(kind), &config.ProviderConfig{Type: kind, BaseURL: server.URL + "/proxy/"}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			lister := provider.(interface {
				ListModels(context.Context) ([]core.ModelInfo, error)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := lister.ListModels(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-seen:
				if got != "/proxy/models" {
					t.Fatalf("path = %q", got)
				}
			case <-ctx.Done():
				t.Fatal("no request")
			}
		})
	}
}
