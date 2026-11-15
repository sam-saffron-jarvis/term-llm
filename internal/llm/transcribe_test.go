package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/openaiapi"
)

func TestTranscribeWithConfig_ChatGPTDispatchesToOAuthBackend(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	audio := filepath.Join(t.TempDir(), "memo.ogg")
	if err := os.WriteFile(audio, []byte("audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Transcription: config.TranscriptionConfig{Model: "ignored-model"}}
	_, err := TranscribeWithConfig(context.Background(), cfg, audio, "en", "chatgpt")
	if err == nil || !strings.Contains(err.Error(), "ChatGPT transcription is not configured") {
		t.Fatalf("TranscribeWithConfig error = %v, want ChatGPT OAuth setup guidance", err)
	}
}

func TestTranscribeWithConfig_UsesResolvedURLForBuiltins(t *testing.T) {
	origClient := defaultHTTPClient
	origProbe := probeAudioDuration
	defer func() {
		defaultHTTPClient = origClient
		probeAudioDuration = origProbe
	}()
	probeAudioDuration = func(context.Context, string) (time.Duration, error) {
		return time.Minute, nil
	}

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
			name: "venice modality override", providerOverride: "venice",
			cfg:     &config.Config{Transcription: config.TranscriptionConfig{Venice: config.TranscriptionVeniceConfig{APIKey: "test-key", BaseURL: "https://feature.example/v1/"}}, Providers: map[string]config.ProviderConfig{"venice": {BaseURL: "https://provider.example/v1"}}},
			wantURL: "https://feature.example/v1/audio/transcriptions",
		},
		{
			name: "elevenlabs modality override", providerOverride: "elevenlabs",
			cfg:     &config.Config{Transcription: config.TranscriptionConfig{ElevenLabs: config.TranscriptionElevenLabsConfig{APIKey: "test-key", BaseURL: "https://feature.example/v1/"}}, Providers: map[string]config.ProviderConfig{"elevenlabs": {BaseURL: "https://provider.example/v1"}}},
			wantURL: "https://feature.example/v1/speech-to-text",
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

func TestTranscribeWithConfig_ElevenLabsPassesTimestampOptions(t *testing.T) {
	origClient := defaultHTTPClient
	origProbe := probeAudioDuration
	defer func() {
		defaultHTTPClient = origClient
		probeAudioDuration = origProbe
	}()
	probeAudioDuration = func(context.Context, string) (time.Duration, error) {
		return time.Minute, nil
	}

	audioFile, err := os.CreateTemp(t.TempDir(), "audio-*.mp3")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	if _, err := audioFile.WriteString("not really audio"); err != nil {
		t.Fatalf("write temp audio: %v", err)
	}
	if err := audioFile.Close(); err != nil {
		t.Fatalf("close temp audio: %v", err)
	}

	var requestBody string
	defaultHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			requestBody = string(body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"text":"hello","words":[{"text":"hello","start":0,"end":0.5,"type":"word","logprob":-0.1}]}`)),
			}, nil
		}),
	}

	result, err := TranscribeWithConfig(context.Background(), &config.Config{
		Transcription: config.TranscriptionConfig{
			Timestamps: true,
		},
		Providers: map[string]config.ProviderConfig{
			"elevenlabs": {ResolvedAPIKey: "test-key"},
		},
	}, audioFile.Name(), "", "elevenlabs")
	if err != nil {
		t.Fatalf("TranscribeWithConfig failed: %v", err)
	}
	for _, want := range []string{"timestamps_granularity", "word", "no_verbatim", "true", "tag_audio_events", "false"} {
		if !strings.Contains(requestBody, want) {
			t.Fatalf("request body missing %q: %s", want, requestBody)
		}
	}
	if !strings.Contains(result, `"words": [`) {
		t.Fatalf("result = %q, want timestamp JSON", result)
	}
}

func TestTranscribeWithConfig_TruncatesImplausiblyLongTranscript(t *testing.T) {
	origClient := defaultHTTPClient
	origProbe := probeAudioDuration
	defer func() {
		defaultHTTPClient = origClient
		probeAudioDuration = origProbe
	}()
	probeAudioDuration = func(context.Context, string) (time.Duration, error) {
		return time.Minute, nil
	}

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

	// 351 words for 1 minute of audio exceeds 350 WPM cap
	longTranscript := strings.TrimSpace(strings.Repeat("word ", 351))
	defaultHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"text":"` + longTranscript + `"}`)),
			}, nil
		}),
	}

	result, err := TranscribeWithConfig(context.Background(), &config.Config{
		Providers: map[string]config.ProviderConfig{
			"openai": {ResolvedAPIKey: "test-key"},
		},
	}, audioFile.Name(), "", "openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be truncated to 350 words (ceil(1 minute * 350 WPM))
	wordCount := len(strings.Fields(result))
	if wordCount != 350 {
		t.Fatalf("word count = %d, want 350 (truncated from 351)", wordCount)
	}
}

func TestTranscribeWithConfig_OpenAIUsesSharedEndpoint(t *testing.T) {
	origClient := defaultHTTPClient
	origProbe := probeAudioDuration
	t.Cleanup(func() {
		defaultHTTPClient = origClient
		probeAudioDuration = origProbe
	})
	probeAudioDuration = func(context.Context, string) (time.Duration, error) { return time.Minute, nil }

	audioPath := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(audioPath, []byte("not really audio"), 0o600); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	tests := []struct {
		name     string
		env      map[string]string
		cfg      *config.Config
		wantURL  string
		wantAuth string
		wantOrg  string
		wantProj string
	}{
		{
			name: "env key with env endpoint",
			env: map[string]string{
				"OPENAI_API_KEY": "env-key", openaiapi.EnvBaseURL: "https://env.example/v1",
				openaiapi.EnvOrganization: "org-env", openaiapi.EnvProject: "proj-env",
			},
			cfg:      &config.Config{},
			wantURL:  "https://env.example/v1/audio/transcriptions",
			wantAuth: "Bearer env-key",
			wantOrg:  "org-env",
			wantProj: "proj-env",
		},
		{
			name: "provider base_url and headers",
			env:  map[string]string{openaiapi.EnvOrganization: "org-env", openaiapi.EnvProject: "proj-env"},
			cfg: &config.Config{Providers: map[string]config.ProviderConfig{
				"openai": {
					ResolvedAPIKey: "config-key",
					BaseURL:        "https://gateway.example/v1",
					Headers:        map[string]string{"OpenAI-Organization": "org-config"},
				},
			}},
			wantURL:  "https://gateway.example/v1/audio/transcriptions",
			wantAuth: "Bearer config-key",
			wantOrg:  "org-config",
			wantProj: "proj-env",
		},
		{
			name: "non-native openai entry keeps its own endpoint and no scoping",
			env:  map[string]string{openaiapi.EnvBaseURL: "https://env.example/v1", openaiapi.EnvOrganization: "org-env"},
			cfg: &config.Config{Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:           config.ProviderTypeOpenAICompat,
					ResolvedAPIKey: "compat-key",
					BaseURL:        "https://compat.example/v1",
				},
			}},
			wantURL:  "https://compat.example/v1/audio/transcriptions",
			wantAuth: "Bearer compat-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"OPENAI_API_KEY", openaiapi.EnvBaseURL, openaiapi.EnvOrganization, openaiapi.EnvProject} {
				t.Setenv(key, tt.env[key])
			}
			var got *http.Request
			defaultHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				got = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"text":"ok"}`)),
				}, nil
			})}

			if _, err := TranscribeWithConfig(context.Background(), tt.cfg, audioPath, "", "openai"); err != nil {
				t.Fatalf("TranscribeWithConfig: %v", err)
			}
			if got == nil {
				t.Fatal("no request sent")
			}
			if got.URL.String() != tt.wantURL || got.Header.Get("Authorization") != tt.wantAuth ||
				got.Header.Get("OpenAI-Organization") != tt.wantOrg || got.Header.Get("OpenAI-Project") != tt.wantProj {
				t.Fatalf("request = %s auth=%q org=%q project=%q", got.URL, got.Header.Get("Authorization"), got.Header.Get("OpenAI-Organization"), got.Header.Get("OpenAI-Project"))
			}
		})
	}
}

func TestChatGPTTranscriptionUsesDedicatedBaseURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	audio := filepath.Join(t.TempDir(), "memo.ogg")
	if err := os.WriteFile(audio, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"chatgpt": {BaseURL: "not-an-http-url"}}}
	_, err := TranscribeWithConfig(context.Background(), cfg, audio, "", "chatgpt")
	if err == nil || !strings.Contains(err.Error(), "ChatGPT transcription is not configured") {
		t.Fatalf("inference root should not affect transcription: %v", err)
	}
	cfg.Transcription.ChatGPT.BaseURL = "not-an-http-url"
	_, err = TranscribeWithConfig(context.Background(), cfg, audio, "", "chatgpt")
	if err == nil || !strings.Contains(err.Error(), "transcription.chatgpt.base_url") {
		t.Fatalf("dedicated root error = %v", err)
	}
}

func TestTranscriptionLocalRoutingAndLazyPrecedence(t *testing.T) {
	t.Setenv("VENICE_API_KEY", "")
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("XI_API_KEY", "")
	t.Setenv("MISTRAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	oldProbe := probeAudioDuration
	probeAudioDuration = func(context.Context, string) (time.Duration, error) { return time.Minute, nil }
	t.Cleanup(func() { probeAudioDuration = oldProbe })
	file := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(file, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"ok"}`)
	}))
	defer server.Close()
	broken := "$(exit 7)"
	for _, kind := range []string{"venice", "elevenlabs"} {
		t.Run(kind+" feature wins", func(t *testing.T) {
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{kind: {BaseURL: broken, URL: broken, APIKey: broken}}}
			want := "/prefix/audio/transcriptions"
			if kind == "venice" {
				cfg.Transcription.Venice.BaseURL = server.URL + "/prefix/"
				cfg.Transcription.Venice.APIKey = "feature-key"
			} else {
				cfg.Transcription.ElevenLabs.BaseURL = server.URL + "/prefix/"
				cfg.Transcription.ElevenLabs.APIKey = "feature-key"
				want = "/prefix/speech-to-text"
			}
			if _, err := TranscribeWithConfig(context.Background(), cfg, file, "", kind); err != nil {
				t.Fatal(err)
			}
			if got := <-paths; got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
		})
	}
	for _, kind := range []string{"local", "mistral", "venice", "elevenlabs", "custom", "openai"} {
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s legacy deferred=%v", kind, deferred), func(t *testing.T) {
				root := server.URL + "/legacy/"
				if deferred {
					root = "$(printf %s '" + root + "')"
				}
				name := kind
				if kind == "local" {
					name = "local_whisper"
				}
				entry := config.ProviderConfig{URL: root, APIKey: "test-key"}
				if kind == "openai" {
					entry.Type = config.ProviderTypeOpenAICompat
				}
				cfg := &config.Config{Providers: map[string]config.ProviderConfig{name: entry}}
				want := "/legacy/audio/transcriptions"
				if kind == "local" {
					want = "/legacy/inference"
				}
				if kind == "elevenlabs" {
					want = "/legacy/speech-to-text"
				}
				if _, err := TranscribeWithConfig(context.Background(), cfg, file, "", kind); err != nil {
					t.Fatal(err)
				}
				if got := <-paths; got != want {
					t.Fatalf("path = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestTranscriptionNamedProviderRequiresEndpoint(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	file := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(file, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	old := defaultHTTPClient
	defaultHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, fmt.Errorf("unexpected request")
	})}
	t.Cleanup(func() { defaultHTTPClient = old })
	for _, name := range []string{"custom", "openai"} {
		for _, root := range []string{"", "$(printf '')"} {
			t.Run(name+"/"+root, func(t *testing.T) {
				cfg := &config.Config{Providers: map[string]config.ProviderConfig{name: {Type: config.ProviderTypeOpenAICompat, APIKey: "private-key", BaseURL: root}}}
				_, err := TranscribeWithConfig(context.Background(), cfg, file, "", name)
				if err == nil || !(strings.Contains(err.Error(), "requires a non-empty") || strings.Contains(err.Error(), "resolved to an empty endpoint")) {
					t.Fatalf("error = %v", err)
				}
			})
		}
	}
}

func TestTranscriptionExplicitEmptyRootDoesNotUsePublicFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VENICE_API_KEY", "")
	t.Setenv("ELEVENLABS_API_KEY", "")
	t.Setenv("XI_API_KEY", "")
	t.Setenv("MISTRAL_API_KEY", "")
	file := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(file, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	old := defaultHTTPClient
	defaultHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, fmt.Errorf("unexpected request")
	})}
	t.Cleanup(func() { defaultHTTPClient = old })
	for _, name := range []string{"local", "mistral", "venice", "elevenlabs", "openai"} {
		t.Run(name, func(t *testing.T) {
			entryName := name
			if name == "local" {
				entryName = "local_whisper"
			}
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{entryName: {URL: "$(printf '')", APIKey: "private-key"}}}
			_, err := TranscribeWithConfig(context.Background(), cfg, file, "", name)
			if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
