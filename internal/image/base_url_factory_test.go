package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestImageFactoryConfiguredRoots(t *testing.T) {
	for _, name := range []string{"gemini", "xai", "venice", "openrouter"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			paths := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths <- r.URL.Path
				http.Error(w, "test", http.StatusBadRequest)
			}))
			defer server.Close()
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{name: {BaseURL: server.URL + "/shared"}}}
			root := server.URL + "/nested/v1/"
			switch name {
			case "gemini":
				cfg.Image.Gemini.APIKey = "key"
				cfg.Image.Gemini.BaseURL = root
			case "xai":
				cfg.Image.XAI.APIKey = "key"
				cfg.Image.XAI.BaseURL = root
			case "venice":
				cfg.Image.Venice.APIKey = "key"
				cfg.Image.Venice.BaseURL = root
			case "openrouter":
				cfg.Image.OpenRouter.APIKey = "key"
				cfg.Image.OpenRouter.BaseURL = root
			}
			p, err := NewImageProvider(cfg, name+":test-model")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = p.Generate(context.Background(), GenerateRequest{Prompt: "test"})
			want := map[string]string{"gemini": "/nested/v1/models/test-model:generateContent", "xai": "/nested/v1/images/generations", "venice": "/nested/v1/image/generate", "openrouter": "/nested/v1/chat/completions"}[name]
			found := false
			close(paths)
			for path := range paths {
				if path == want {
					found = true
				} else {
					t.Errorf("unexpected path %q", path)
				}
			}
			if !found {
				t.Errorf("missing request to %s", want)
			}
		})
	}
}

func TestOpenAIImageFactoryFeatureRootSkipsSharedDeferredRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feature/images/generations" || r.Header.Get("OpenAI-Project") != "project" {
			t.Errorf("request = %s, headers = %v", r.URL, r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"cG5n"}]}`))
	}))
	defer server.Close()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"openai": {BaseURL: "file://" + t.TempDir() + "/missing", Headers: map[string]string{"OpenAI-Project": "project"}}}}
	cfg.Image.OpenAI.APIKey = "key"
	cfg.Image.OpenAI.BaseURL = server.URL + "/feature/"
	p, err := NewImageProvider(cfg, "openai")
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Generate(context.Background(), GenerateRequest{Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "png" {
		t.Fatalf("data = %q", result.Data)
	}
}

func TestImageFactoryEscapesModelSegment(t *testing.T) {
	for _, name := range []string{"gemini", "flux"} {
		t.Run(name, func(t *testing.T) {
			const model = "special?query#fragment/segment%"
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				want := "/nested/v1/" + url.PathEscape(model)
				if name == "gemini" {
					want = "/nested/v1/models/" + url.PathEscape(model) + ":generateContent"
				}
				if r.URL.EscapedPath() != want || r.URL.RawQuery != "" {
					t.Errorf("URL=%s", r.URL)
				}
				http.Error(w, "test", 400)
			}))
			defer server.Close()
			cfg := &config.Config{}
			if name == "gemini" {
				cfg.Image.Gemini.APIKey = "key"
				cfg.Image.Gemini.BaseURL = server.URL + "/nested/v1"
			} else {
				cfg.Image.Flux.APIKey = "key"
				cfg.Image.Flux.BaseURL = server.URL + "/nested/v1"
			}
			p, err := NewImageProvider(cfg, name+":"+model)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Generate(context.Background(), GenerateRequest{Prompt: "hello"}); err == nil {
				t.Fatal("expected HTTP error")
			}
			if !called {
				t.Fatal("missing request")
			}
		})
	}
}
