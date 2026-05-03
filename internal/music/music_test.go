package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVeniceGenerateQueuesAndRetrievesAudio(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio/queue":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode queue payload: %v", err)
			}
			if payload["model"] != "elevenlabs-sound-effects-v2" || payload["prompt"] != "tiny bell" || payload["duration_seconds"].(float64) != 1 {
				t.Fatalf("unexpected queue payload: %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": payload["model"], "queue_id": "q1", "status": "QUEUED"})
		case "/audio/retrieve":
			calls++
			if calls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "PROCESSING"})
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3audio"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewVeniceProvider("key")
	provider.baseURL = server.URL
	result, err := provider.Generate(context.Background(), Request{Prompt: "tiny bell", Model: "elevenlabs-sound-effects-v2", DurationSeconds: 1, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if string(result.Data) != "ID3audio" || result.MimeType != "audio/mpeg" || result.Format != "mp3" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestVeniceQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/quote" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"quote": 0.0023})
	}))
	defer server.Close()

	provider := NewVeniceProvider("key")
	provider.baseURL = server.URL
	result, err := provider.Generate(context.Background(), Request{Prompt: "x", Model: "elevenlabs-sound-effects-v2", DurationSeconds: 1, QuoteOnly: true})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Quote == nil || *result.Quote != 0.0023 {
		t.Fatalf("unexpected quote: %#v", result.Quote)
	}
}

func TestElevenLabsGenerateMusic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/music" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("output_format") != "mp3_44100_128" {
			t.Fatalf("unexpected output format: %s", r.URL.RawQuery)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["prompt"] != "funk sting" || payload["model_id"] != "music_v1" || payload["music_length_ms"].(float64) != 3000 || payload["force_instrumental"] != true {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3music"))
	}))
	defer server.Close()

	provider := NewElevenLabsProvider("key")
	provider.baseURL = server.URL
	result, err := provider.Generate(context.Background(), Request{Prompt: "funk sting", Model: "music_v1", Format: "mp3_44100_128", DurationSeconds: 3, ForceInstrumental: true, ForceInstrumentalSet: true})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if string(result.Data) != "ID3music" || result.MimeType != "audio/mpeg" || result.Format != "mp3_44100_128" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
