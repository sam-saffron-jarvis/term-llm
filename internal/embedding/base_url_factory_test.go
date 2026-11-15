package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestFactoryConfiguredBaseURLs(t *testing.T) {
	for _, provider := range []string{"gemini", "jina", "voyage", "openai", "ollama", "venice"} {
		t.Run(provider, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				switch provider {
				case "gemini":
					_, _ = w.Write([]byte(`{"embeddings":[{"values":[1]}]}`))
				case "ollama":
					_, _ = w.Write([]byte(`{"embeddings":[[1]]}`))
				default:
					_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
				}
			}))
			defer server.Close()
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{provider: {BaseURL: server.URL + "/shared/", APIKey: "test-key"}}}
			root := server.URL + "/nested/v1/"
			switch provider {
			case "gemini":
				cfg.Embed.Gemini.APIKey = "test-key"
				cfg.Embed.Gemini.BaseURL = root
			case "jina":
				cfg.Embed.Jina.APIKey = "test-key"
				cfg.Embed.Jina.BaseURL = root
			case "voyage":
				cfg.Embed.Voyage.APIKey = "test-key"
				cfg.Embed.Voyage.BaseURL = root
			case "openai":
				cfg.Embed.OpenAI.APIKey = "test-key"
				cfg.Embed.OpenAI.BaseURL = root
			case "ollama":
				cfg.Embed.Ollama.BaseURL = root
			case "venice":
				root = server.URL + "/shared/"
			}
			p, err := NewEmbeddingProvider(cfg, provider)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}, Model: "test-model"}); err != nil {
				t.Fatal(err)
			}
			want := "/nested/v1/embeddings"
			if provider == "gemini" {
				want = "/nested/v1/models/test-model:batchEmbedContents"
			}
			if provider == "ollama" {
				want = "/nested/v1/api/embed"
			}
			if provider == "venice" {
				want = "/shared/embeddings"
			}
			if gotPath != want {
				t.Fatalf("path = %q, want %q", gotPath, want)
			}
		})
	}
}

func TestOpenAIFactoryFeatureRootSkipsSharedDeferredRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feature/embeddings" || r.Header.Get("OpenAI-Project") != "project" {
			t.Errorf("request = %s, headers = %v", r.URL, r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer server.Close()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"openai": {BaseURL: "file://" + t.TempDir() + "/missing", Headers: map[string]string{"OpenAI-Project": "project"}}}}
	cfg.Embed.OpenAI.APIKey = "key"
	cfg.Embed.OpenAI.BaseURL = server.URL + "/feature/"
	p, err := NewEmbeddingProvider(cfg, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryGeminiEscapesModelSegment(t *testing.T) {
	const model = "special?query#fragment/segment%"
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.EscapedPath() != "/nested/models/"+url.PathEscape(model)+":batchEmbedContents" || r.URL.RawQuery != "" {
			t.Errorf("URL=%s", r.URL)
		}
		http.Error(w, "test", 400)
	}))
	defer server.Close()
	cfg := &config.Config{}
	cfg.Embed.Gemini.APIKey = "key"
	cfg.Embed.Gemini.BaseURL = server.URL + "/nested/"
	p, err := NewEmbeddingProvider(cfg, "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}, Model: model}); err == nil {
		t.Fatal("expected HTTP error")
	}
	if !called {
		t.Fatal("missing request")
	}
}
