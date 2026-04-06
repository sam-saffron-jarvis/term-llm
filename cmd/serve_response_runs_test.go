package cmd

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestAppendResponseRunEventUsesToolCallIDForFunctionCallItems(t *testing.T) {
	run := newResponseRun("resp_toolcall", "sess_test", "", "mock", time.Now().Unix(), func() {})
	state := &responseRunStreamState{}

	err := (&serveServer{}).appendResponseRunEvent(&serveRuntime{}, run, state, llm.Event{
		Type:       llm.EventToolCall,
		ToolCallID: "call_123",
		Tool: &llm.ToolCall{
			Name:      "fetch_url",
			Arguments: json.RawMessage(`{"url":"https://example.com"}`),
		},
	})
	if err != nil {
		t.Fatalf("appendResponseRunEvent failed: %v", err)
	}

	if len(run.events) < 1 {
		t.Fatal("expected stored events")
	}
	if run.events[0].Event != "response.output_item.added" {
		t.Fatalf("expected first event to be response.output_item.added, got %s", run.events[0].Event)
	}

	var payload struct {
		Item struct {
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
	}
	if err := json.Unmarshal(run.events[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal stored event payload: %v", err)
	}
	if payload.Item.ID != "fc_call_123" {
		t.Fatalf("expected item id fc_call_123, got %q", payload.Item.ID)
	}
	if payload.Item.CallID != "call_123" {
		t.Fatalf("expected call_id call_123, got %q", payload.Item.CallID)
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.recoveryMessages) != 1 {
		t.Fatalf("expected 1 recovery message, got %d", len(run.recoveryMessages))
	}
	if len(run.recoveryMessages[0].Tools) != 1 {
		t.Fatalf("expected 1 recovery tool, got %d", len(run.recoveryMessages[0].Tools))
	}
	if run.recoveryMessages[0].Tools[0].ID != "call_123" {
		t.Fatalf("expected recovery tool ID call_123, got %q", run.recoveryMessages[0].Tools[0].ID)
	}
}

func TestResponseRunSubscriberSurvivesUpToBufferLimit(t *testing.T) {
	run := newResponseRun("resp_test1", "sess_test", "", "mock", time.Now().Unix(), func() {})

	sub := run.subscribe(0)
	if sub.ch == nil {
		t.Fatal("expected live channel from subscribe")
	}
	defer run.unsubscribe(sub.ch)

	// Fill the subscriber buffer up to the limit (should not drop)
	for i := 0; i < defaultResponseRunSubscriberBuffer; i++ {
		err := run.appendEvent("response.output_text.delta", map[string]any{
			"delta": "x",
		})
		if err != nil {
			t.Fatalf("appendEvent failed at %d: %v", i, err)
		}
	}

	// Subscriber should still be registered
	run.mu.Lock()
	subCount := len(run.subscribers)
	run.mu.Unlock()

	if subCount != 1 {
		t.Fatalf("expected 1 subscriber, got %d", subCount)
	}

	// Drain all events from the channel
	for i := 0; i < defaultResponseRunSubscriberBuffer; i++ {
		select {
		case ev := <-sub.ch:
			if ev.Sequence != int64(i+1) {
				t.Fatalf("expected sequence %d, got %d", i+1, ev.Sequence)
			}
		default:
			t.Fatalf("expected event at index %d but channel was empty", i)
		}
	}
}

func TestResponseRunSubscriberDroppedWhenBufferFull(t *testing.T) {
	run := newResponseRun("resp_test2", "sess_test", "", "mock", time.Now().Unix(), func() {})

	sub := run.subscribe(0)
	if sub.ch == nil {
		t.Fatal("expected live channel from subscribe")
	}
	defer run.unsubscribe(sub.ch)

	// Fill buffer completely
	for i := 0; i < defaultResponseRunSubscriberBuffer; i++ {
		if err := run.appendEvent("response.output_text.delta", map[string]any{"delta": "x"}); err != nil {
			t.Fatalf("appendEvent failed at %d: %v", i, err)
		}
	}

	// One more should drop the subscriber
	if err := run.appendEvent("response.output_text.delta", map[string]any{"delta": "overflow"}); err != nil {
		t.Fatalf("appendEvent failed on overflow: %v", err)
	}

	run.mu.Lock()
	subCount := len(run.subscribers)
	run.mu.Unlock()
	if subCount != 0 {
		t.Fatalf("expected 0 subscribers after overflow, got %d", subCount)
	}
}

func TestResponseRunConcurrentAppendsPreserveOrder(t *testing.T) {
	const totalEvents = 200
	const numWriters = 4
	eventsPerWriter := totalEvents / numWriters

	run := newResponseRun("resp_order", "sess_test", "", "mock", time.Now().Unix(), func() {})

	sub := run.subscribe(0)
	if sub.ch == nil {
		t.Fatal("expected live channel from subscribe")
	}
	defer run.unsubscribe(sub.ch)

	// Launch concurrent writers, each appending eventsPerWriter events.
	var wg sync.WaitGroup
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerWriter; i++ {
				_ = run.appendEvent("response.output_text.delta", map[string]any{"delta": "x"})
			}
		}()
	}

	// Collect all events from subscriber in a separate goroutine.
	received := make([]int64, 0, totalEvents)
	done := make(chan struct{})
	go func() {
		for ev := range sub.ch {
			received = append(received, ev.Sequence)
			if len(received) >= totalEvents {
				break
			}
		}
		close(done)
	}()

	wg.Wait()

	// Wait for all events to arrive (or timeout)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for events, got %d/%d", len(received), totalEvents)
	}

	// Verify strictly increasing sequence numbers
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Fatalf("out of order at index %d: seq %d followed by %d", i, received[i-1], received[i])
		}
	}

	// Verify no gaps
	for i, seq := range received {
		expected := int64(i + 1)
		if seq != expected {
			t.Fatalf("gap at index %d: expected seq %d, got %d", i, expected, seq)
		}
	}
}
