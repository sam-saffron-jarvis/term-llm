package audio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestConfiguredProvidersRequests(t *testing.T) {
	for _, name := range []string{"venice", "gemini", "elevenlabs"} {
		t.Run(name, func(t *testing.T) {
			var paths []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.URL.Path == "/proxy/v2/voices" {
					fmt.Fprint(w, `{"voices":[{"name":"Alice","voice_id":"12345678901234567890"}]}`)
					return
				}
				http.Error(w, "test", 400)
			}))
			defer s.Close()
			cfg := &config.Config{}
			root := s.URL + "/proxy/"
			switch name {
			case "venice":
				cfg.Audio.Venice.BaseURL = root
				p, err := NewConfiguredVeniceProvider(cfg, "key")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Generate(context.Background(), Request{Input: "hi"})
			case "gemini":
				cfg.Audio.Gemini.BaseURL = root
				p, err := NewConfiguredGeminiProvider(cfg, "key")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Generate(context.Background(), Request{Input: "hi", Model: "model"})
			case "elevenlabs":
				cfg.Audio.ElevenLabs.BaseURL = root
				p, err := NewConfiguredElevenLabsProvider(cfg, "key")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Generate(context.Background(), Request{Input: "hi", Voice: "Alice"})
			}
			want := map[string][]string{"venice": {"/proxy/audio/speech"}, "gemini": {"/proxy/models/model:generateContent"}, "elevenlabs": {"/proxy/v2/voices", "/proxy/v1/text-to-speech/12345678901234567890"}}[name]
			if fmt.Sprint(paths) != fmt.Sprint(want) {
				t.Fatalf("paths %v, want %v", paths, want)
			}
		})
	}
}

func TestConfiguredGeminiEscapesModelSegment(t *testing.T) {
	const model = "special?query#fragment/segment%"
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.EscapedPath() != "/nested/models/"+url.PathEscape(model)+":generateContent" || r.URL.RawQuery != "" {
			t.Errorf("URL=%s", r.URL)
		}
		http.Error(w, "test", 400)
	}))
	defer server.Close()
	cfg := &config.Config{}
	cfg.Audio.Gemini.BaseURL = server.URL + "/nested/"
	p, err := NewConfiguredGeminiProvider(cfg, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Generate(context.Background(), Request{Input: "hello", Model: model}); err == nil {
		t.Fatal("expected HTTP error")
	}
	if !called {
		t.Fatal("missing request")
	}
}
