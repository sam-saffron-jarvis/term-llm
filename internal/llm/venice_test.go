package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVeniceProviderCapabilities(t *testing.T) {
	provider := NewVeniceProvider("key", "")
	caps := provider.Capabilities()
	if !caps.NativeWebSearch {
		t.Fatal("expected NativeWebSearch=true")
	}
	if caps.NativeWebFetch {
		t.Fatal("expected NativeWebFetch=false")
	}
	if !caps.ToolCalls {
		t.Fatal("expected ToolCalls=true")
	}
	if !caps.SupportsToolChoice {
		t.Fatal("expected SupportsToolChoice=true")
	}
}

func TestAppendVeniceModelSuffix(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		suffix string
		want   string
	}{
		{name: "plain model", model: "venice-uncensored", suffix: "enable_web_search=on", want: "venice-uncensored:enable_web_search=on"},
		{name: "existing suffix", model: "grok-4-20-beta:enable_x_search=true", suffix: "enable_web_search=on", want: "grok-4-20-beta:enable_x_search=true&enable_web_search=on"},
		{name: "empty suffix", model: "venice-uncensored", suffix: "", want: "venice-uncensored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendVeniceModelSuffix(tt.model, tt.suffix); got != tt.want {
				t.Fatalf("appendVeniceModelSuffix(%q, %q) = %q, want %q", tt.model, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestVeniceProviderSearchUsesModelSuffixAndDisablesParallelToolCalls(t *testing.T) {
	var got struct {
		Model             string `json:"model"`
		ParallelToolCalls *bool  `json:"parallel_tool_calls,omitempty"`
		Stream            bool   `json:"stream"`
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer ts.Close()

	provider := &VeniceProvider{OpenAICompatProvider: NewOpenAICompatProvider(ts.URL, "test-key", "venice-uncensored", "Venice")}
	stream, err := provider.Stream(context.Background(), Request{
		Messages:          []Message{UserText("latest Venice news")},
		Search:            true,
		ParallelToolCalls: true,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		if ev.Type == EventDone {
			break
		}
	}

	if got.Model != "venice-uncensored:enable_web_search=on" {
		t.Fatalf("expected search model suffix, got %q", got.Model)
	}
	if got.ParallelToolCalls != nil {
		t.Fatalf("expected parallel_tool_calls to be omitted/false, got %+v", got.ParallelToolCalls)
	}
	if !got.Stream {
		t.Fatal("expected stream=true")
	}
}
