package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

type retryStreamingProvider struct {
	attempt int
}

type syncToolProvider struct{}

func (p *retryStreamingProvider) Name() string { return "retry-streaming" }

func (p *syncToolProvider) Name() string { return "sync-tool" }

func (p *retryStreamingProvider) Credential() string { return "mock" }

func (p *syncToolProvider) Credential() string { return "mock" }

func (p *retryStreamingProvider) Capabilities() Capabilities { return Capabilities{} }

func (p *syncToolProvider) Capabilities() Capabilities { return Capabilities{} }

func (p *retryStreamingProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	p.attempt++
	if p.attempt == 1 {
		return newEventStream(ctx, func(ctx context.Context, ch chan<- Event) error {
			ch <- Event{Type: EventTextDelta, Text: "hello"}
			ch <- Event{Type: EventError, Err: &RateLimitError{Message: "rate limit", RetryAfter: 5 * time.Millisecond}}
			return nil
		}), nil
	}
	return newEventStream(ctx, func(ctx context.Context, ch chan<- Event) error {
		ch <- Event{Type: EventTextDelta, Text: "hello"}
		ch <- Event{Type: EventTextDelta, Text: " world"}
		return nil
	}), nil
}

func (p *syncToolProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	return newEventStream(ctx, func(ctx context.Context, ch chan<- Event) error {
		responseCh := make(chan ToolExecutionResponse, 1)
		ch <- Event{
			Type:         EventToolCall,
			ToolCallID:   "tool-1",
			ToolName:     "read_file",
			Tool:         &ToolCall{ID: "tool-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"/tmp/test.txt"}`)},
			ToolResponse: responseCh,
		}

		select {
		case resp := <-responseCh:
			if resp.Err != nil {
				return resp.Err
			}
			ch <- Event{Type: EventTextDelta, Text: resp.Result.Content}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}), nil
}

func TestIsRetryable_500InternalServerError(t *testing.T) {
	cases := []struct {
		msg       string
		retryable bool
	}{
		{"anthropic streaming error: POST \"https://api.anthropic.com/v1/messages\": 500 Internal Server Error", true},
		{"500 internal server error", true},
		{"got 500 from upstream", true},
		{"internal server error occurred", true},
		{"400 Bad Request", false},
		{"401 Unauthorized", false},
	}
	for _, tc := range cases {
		got := isRetryable(errors.New(tc.msg))
		if got != tc.retryable {
			t.Errorf("isRetryable(%q) = %v, want %v", tc.msg, got, tc.retryable)
		}
	}
}

// toolThenErrorProvider emits a synchronous tool call then a retryable error.
// The retry loop must NOT retry after the tool call has been committed.
type toolThenErrorProvider struct {
	attempts int
}

func (p *toolThenErrorProvider) Name() string       { return "tool-then-error" }
func (p *toolThenErrorProvider) Credential() string { return "mock" }
func (p *toolThenErrorProvider) Capabilities() Capabilities {
	return Capabilities{}
}

func (p *toolThenErrorProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	p.attempts++
	return newEventStream(ctx, func(ctx context.Context, ch chan<- Event) error {
		// Emit a synchronous tool call (has ToolResponse channel)
		response := make(chan ToolExecutionResponse, 1)
		ch <- Event{
			Type:         EventToolCall,
			ToolResponse: response,
			ToolName:     "test_tool",
		}
		// Simulate tool execution completing
		response <- ToolExecutionResponse{Result: ToolOutput{Content: "tool result"}}
		// Then a retryable error occurs
		ch <- Event{Type: EventError, Err: errors.New("502 bad gateway")}
		return nil
	}), nil
}

func TestRetryProvider_DoesNotRetryAfterCommittedToolCall(t *testing.T) {
	inner := &toolThenErrorProvider{}
	provider := WrapWithRetry(inner, RetryConfig{
		MaxAttempts: 3,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
	})

	stream, err := provider.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // error is expected — the committed error propagates
		}
	}

	if inner.attempts != 1 {
		t.Fatalf("inner.attempts = %d, want 1 (should not retry after committed tool call)", inner.attempts)
	}
}

func TestRetryProvider_DropsPartialTextFromRetriedAttempt(t *testing.T) {
	provider := WrapWithRetry(&retryStreamingProvider{}, RetryConfig{
		MaxAttempts: 2,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
	})

	stream, err := provider.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	var text string
	retryEvents := 0
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv returned error: %v", err)
		}
		switch event.Type {
		case EventTextDelta:
			text += event.Text
		case EventRetry:
			retryEvents++
		case EventError:
			t.Fatalf("unexpected final error event: %v", event.Err)
		}
	}

	if retryEvents != 1 {
		t.Fatalf("retryEvents = %d, want 1", retryEvents)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestRetryProvider_ForwardsSyncToolCallsImmediately(t *testing.T) {
	provider := WrapWithRetry(&syncToolProvider{}, RetryConfig{
		MaxAttempts: 2,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
	})

	stream, err := provider.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	gotTool := make(chan Event, 1)
	errCh := make(chan error, 1)
	go func() {
		event, err := stream.Recv()
		if err != nil {
			errCh <- err
			return
		}
		gotTool <- event
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Recv returned error before tool event: %v", err)
	case event := <-gotTool:
		if event.Type != EventToolCall {
			t.Fatalf("first event type = %v, want %v", event.Type, EventToolCall)
		}
		if event.ToolResponse == nil {
			t.Fatal("expected ToolResponse channel on sync tool event")
		}
		event.ToolResponse <- ToolExecutionResponse{Result: ToolOutput{Content: "alpha"}}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for sync tool event; retry wrapper buffered it")
	}

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv after tool response returned error: %v", err)
	}
	if event.Type != EventTextDelta || event.Text != "alpha" {
		t.Fatalf("got event %+v, want text_delta alpha", event)
	}
}
