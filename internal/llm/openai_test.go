package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProviderStreamSendsExplicitParallelToolCallsFalse(t *testing.T) {
	var got struct {
		ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
		Tools             []json.RawMessage `json:"tools,omitempty"`
		Stream            bool              `json:"stream"`
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer ts.Close()

	provider := &OpenAIProvider{
		apiKey: "test-key",
		model:  "gpt-4.1",
		responsesClient: &ResponsesClient{
			BaseURL:       ts.URL,
			GetAuthHeader: func() string { return "Bearer test-key" },
			HTTPClient:    ts.Client(),
		},
	}

	stream, err := provider.Stream(context.Background(), Request{
		Messages: []Message{UserText("hello")},
		Tools: []ToolSpec{{
			Name:        "echo",
			Description: "Echo input",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{"type": "string"},
				},
			},
		}},
		ParallelToolCalls: false,
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

	if got.ParallelToolCalls == nil {
		t.Fatal("expected parallel_tool_calls to be sent explicitly")
	}
	if *got.ParallelToolCalls {
		t.Fatalf("expected parallel_tool_calls=false, got %v", *got.ParallelToolCalls)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(got.Tools))
	}
	if !got.Stream {
		t.Fatal("expected stream=true")
	}
}

func TestOpenAIProviderStreamSendsExplicitZeroTemperatureAndTopP(t *testing.T) {
	var got struct {
		Temperature *float64 `json:"temperature,omitempty"`
		TopP        *float64 `json:"top_p,omitempty"`
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer ts.Close()

	provider := &OpenAIProvider{
		apiKey: "test-key",
		model:  "gpt-4.1",
		responsesClient: &ResponsesClient{
			BaseURL:       ts.URL,
			GetAuthHeader: func() string { return "Bearer test-key" },
			HTTPClient:    ts.Client(),
		},
	}

	stream, err := provider.Stream(context.Background(), Request{
		Messages:       []Message{UserText("hello")},
		Temperature:    0,
		TemperatureSet: true,
		TopP:           0,
		TopPSet:        true,
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

	if got.Temperature == nil {
		t.Fatal("expected temperature=0 to be sent explicitly")
	}
	if *got.Temperature != 0 {
		t.Fatalf("expected temperature=0, got %v", *got.Temperature)
	}
	if got.TopP == nil {
		t.Fatal("expected top_p=0 to be sent explicitly")
	}
	if *got.TopP != 0 {
		t.Fatalf("expected top_p=0, got %v", *got.TopP)
	}
}
