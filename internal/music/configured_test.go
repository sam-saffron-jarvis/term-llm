package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestConfiguredProvidersRequests(t *testing.T) {
	for _, name := range []string{"venice", "elevenlabs"} {
		t.Run(name, func(t *testing.T) {
			var path string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; http.Error(w, "test", 400) }))
			defer s.Close()
			cfg := &config.Config{}
			if name == "venice" {
				cfg.Music.Venice.BaseURL = s.URL + "/proxy/"
				p, err := NewConfiguredVeniceProvider(cfg, "key")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Generate(context.Background(), Request{Prompt: "hi"})
			} else {
				cfg.Music.ElevenLabs.BaseURL = s.URL + "/proxy/"
				p, err := NewConfiguredElevenLabsProvider(cfg, "key")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Generate(context.Background(), Request{Prompt: "hi"})
			}
			want := "/proxy/audio/queue"
			if name == "elevenlabs" {
				want = "/proxy/v1/music"
			}
			if path != want {
				t.Fatalf("path %q want %q", path, want)
			}
		})
	}
}
