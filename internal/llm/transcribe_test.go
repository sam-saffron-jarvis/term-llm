package llm

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestTranscribeWithConfig_UsesResolvedURLForBuiltins(t *testing.T) {
	origClient := defaultHTTPClient
	defer func() {
		defaultHTTPClient = origClient
	}()

	audioFile, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	if _, err := audioFile.WriteString("not really audio"); err != nil {
		t.Fatalf("write temp audio: %v", err)
	}
	if err := audioFile.Close(); err != nil {
		t.Fatalf("close temp audio: %v", err)
	}

	tests := []struct {
		name             string
		providerOverride string
		cfg              *config.Config
		wantURL          string
	}{
		{
			name:             "openai",
			providerOverride: "openai",
			cfg: &config.Config{
				Providers: map[string]config.ProviderConfig{
					"openai": {
						BaseURL:        "https://base.example.test/v1",
						ResolvedURL:    "https://resolved.example.test/v1",
						ResolvedAPIKey: "test-key",
					},
				},
			},
			wantURL: "https://resolved.example.test/v1/audio/transcriptions",
		},
		{
			name:             "mistral",
			providerOverride: "mistral",
			cfg: &config.Config{
				Providers: map[string]config.ProviderConfig{
					"mistral": {
						BaseURL:        "https://base.mistral.test/v1",
						ResolvedURL:    "https://resolved.mistral.test/v1",
						ResolvedAPIKey: "test-key",
					},
				},
			},
			wantURL: "https://resolved.mistral.test/v1/audio/transcriptions",
		},
		{
			name:             "local",
			providerOverride: "local",
			cfg: &config.Config{
				Providers: map[string]config.ProviderConfig{
					"local_whisper": {
						BaseURL:     "https://base.local.test/v1",
						ResolvedURL: "https://resolved.local.test/v1",
					},
				},
			},
			wantURL: "https://resolved.local.test/v1/inference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capturedURL := ""
			defaultHTTPClient = &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					capturedURL = req.URL.String()
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"text":"ok"}`)),
					}, nil
				}),
			}

			text, err := TranscribeWithConfig(context.Background(), tt.cfg, audioFile.Name(), "", tt.providerOverride)
			if err != nil {
				t.Fatalf("TranscribeWithConfig failed: %v", err)
			}
			if text != "ok" {
				t.Fatalf("text = %q, want %q", text, "ok")
			}
			if capturedURL != tt.wantURL {
				t.Fatalf("request URL = %q, want %q", capturedURL, tt.wantURL)
			}
		})
	}
}
