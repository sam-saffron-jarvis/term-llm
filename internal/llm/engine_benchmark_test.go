package llm

import (
	"context"
	"encoding/json"
	"testing"
)

type fastBenchmarkTool struct{}

func (t *fastBenchmarkTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "fast_benchmark_tool",
		Description: "fast no-op benchmark tool",
		Schema:      map[string]any{"type": "object"},
	}
}

func (t *fastBenchmarkTool) Execute(ctx context.Context, args json.RawMessage) (ToolOutput, error) {
	return TextOutput("ok"), nil
}

func (t *fastBenchmarkTool) Preview(args json.RawMessage) string {
	return ""
}

func BenchmarkExecuteSingleToolCallFast(b *testing.B) {
	registry := NewToolRegistry()
	registry.Register(&fastBenchmarkTool{})
	engine := NewEngine(&fakeProvider{}, registry)
	call := ToolCall{
		ID:        "call-bench",
		Name:      "fast_benchmark_tool",
		Arguments: json.RawMessage(`{}`),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 1024)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-events:
			case <-ctx.Done():
				return
			}
		}
	}()
	send := eventSender{ctx: ctx, ch: events}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs, err := engine.executeSingleToolCall(ctx, call, send, false, false)
		if err != nil {
			b.Fatalf("executeSingleToolCall returned error: %v", err)
		}
		if len(msgs) != 1 {
			b.Fatalf("executeSingleToolCall returned %d messages, want 1", len(msgs))
		}
	}
	b.StopTimer()
	cancel()
	<-drained
}
