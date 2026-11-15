package embedding

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/credentials"
)

func TestChatGPTProviderUsesOAuthTokenAndModel(t *testing.T) {
	creds := &credentials.ChatGPTCredentials{AccessToken: "oauth-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	provider := NewChatGPTProvider()
	provider.model = "text-embedding-3-large"
	provider.loadCredentials = func() (*credentials.ChatGPTCredentials, error) { return creds, nil }
	provider.refresh = func(*credentials.ChatGPTCredentials) error {
		t.Fatal("unexpected token refresh")
		return nil
	}
	provider.embed = func(_ context.Context, token, model string, req EmbedRequest) (*EmbeddingResult, error) {
		if token != "oauth-token" {
			t.Fatalf("token = %q", token)
		}
		if model != "text-embedding-3-large" {
			t.Fatalf("model = %q", model)
		}
		if len(req.Texts) != 1 || req.Texts[0] != "hello" {
			t.Fatalf("request = %#v", req)
		}
		return &EmbeddingResult{Model: model, Dimensions: 3072}, nil
	}

	result, err := provider.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if result.Model != "text-embedding-3-large" || result.Dimensions != 3072 {
		t.Fatalf("result = %#v", result)
	}
}

func TestChatGPTProviderRefreshesExpiredToken(t *testing.T) {
	creds := &credentials.ChatGPTCredentials{AccessToken: "expired", ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	provider := NewChatGPTProvider()
	provider.loadCredentials = func() (*credentials.ChatGPTCredentials, error) { return creds, nil }
	provider.refresh = func(got *credentials.ChatGPTCredentials) error {
		if got != creds {
			t.Fatal("refresh received different credentials")
		}
		got.AccessToken = "refreshed"
		got.ExpiresAt = time.Now().Add(time.Hour).Unix()
		return nil
	}
	provider.embed = func(_ context.Context, token, _ string, _ EmbedRequest) (*EmbeddingResult, error) {
		if token != "refreshed" {
			t.Fatalf("token = %q", token)
		}
		return &EmbeddingResult{}, nil
	}

	if _, err := provider.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
}

func TestChatGPTProviderReportsCredentialFailure(t *testing.T) {
	provider := NewChatGPTProvider()
	provider.loadCredentials = func() (*credentials.ChatGPTCredentials, error) {
		return nil, errors.New("missing")
	}
	if _, err := provider.Embed(context.Background(), EmbedRequest{}); err == nil {
		t.Fatal("expected credential error")
	}
}

func TestChatGPTFactoryDedicatedEndpoint(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://platform.invalid/v1")
	t.Setenv("OPENAI_ORG_ID", "platform-org")
	t.Setenv("OPENAI_PROJECT_ID", "platform-project")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/v1/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-test" {
			t.Errorf("authorization = %q", got)
		}
		if r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("OpenAI-Project") != "" {
			t.Error("platform headers leaked")
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer server.Close()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"chatgpt": {BaseURL: "https://codex.invalid/backend-api/codex"}}}
	cfg.Embed.ChatGPT.BaseURL = server.URL + "/proxy/v1/"
	provider, err := NewEmbeddingProvider(cfg, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*ChatGPTProvider)
	p.loadCredentials = func() (*credentials.ChatGPTCredentials, error) {
		return &credentials.ChatGPTCredentials{AccessToken: "oauth-test", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
	}
	if _, err := p.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
}

func TestChatGPTFactoryDefaultsIgnorePlatformAndCodex(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://platform.invalid/v1")
	t.Setenv("OPENAI_ORG_ID", "platform-org")
	t.Setenv("OPENAI_PROJECT_ID", "platform-project")
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/embeddings" {
			t.Errorf("url = %q", r.URL)
		}
		if r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("OpenAI-Project") != "" {
			t.Error("platform headers leaked")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[1]}]}`)), Request: r}, nil
	})
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"chatgpt": {BaseURL: "https://codex.invalid/backend-api/codex"}}}
	provider, err := NewEmbeddingProvider(cfg, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*ChatGPTProvider)
	p.loadCredentials = func() (*credentials.ChatGPTCredentials, error) {
		return &credentials.ChatGPTCredentials{AccessToken: "oauth-test", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
	}
	if _, err := p.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
