package llm

import (
	"context"
	"testing"
	"time"
)

func TestBuildGeminiContents_DropsDanglingToolCalls(t *testing.T) {
	_, contents := buildGeminiContents([]Message{
		UserText("Run shell"),
		{
			Role: RoleAssistant,
			Parts: []Part{
				{Type: PartText, Text: "Working"},
				{
					Type: PartToolCall,
					ToolCall: &ToolCall{
						ID:        "call-1",
						Name:      "shell",
						Arguments: []byte(`{"command":"sleep 10"}`),
					},
				},
			},
		},
		UserText("new request"),
	})

	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}

	assistant := contents[1]
	if assistant.Role != "model" {
		t.Fatalf("expected role model, got %q", assistant.Role)
	}

	var sawText bool
	for _, part := range assistant.Parts {
		if part.FunctionCall != nil {
			t.Fatalf("expected dangling functionCall to be removed, got %#v", part.FunctionCall)
		}
		if part.Text == "Working" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("expected assistant text to be preserved, got %#v", assistant.Parts)
	}
}

func TestEmitGeminiEvent_ReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 1)
	events <- Event{Type: EventTextDelta, Text: "buffer full"}
	cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- emitGeminiEvent(ctx, events, Event{Type: EventDone})
	}()

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("emitGeminiEvent() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("emitGeminiEvent() blocked after context cancellation")
	}
}
