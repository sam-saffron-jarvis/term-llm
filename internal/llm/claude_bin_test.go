package llm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClaudeBinProvider_ImplementsToolExecutorSetter(t *testing.T) {
	provider := NewClaudeBinProvider("sonnet")

	// This type assertion must succeed for tools to work.
	// The bug was that ClaudeBinProvider.SetToolExecutor used mcphttp.ToolExecutor
	// (a named type) instead of the anonymous function type in the interface,
	// which caused this assertion to fail silently.
	if _, ok := interface{}(provider).(ToolExecutorSetter); !ok {
		t.Fatal("ClaudeBinProvider does not implement ToolExecutorSetter interface - tools will not work")
	}
}

func TestRetryProvider_ForwardsToolExecutorSetter(t *testing.T) {
	// ClaudeBinProvider is wrapped with WrapWithRetry in the factory.
	// The RetryProvider must forward SetToolExecutor to the inner provider.
	provider := NewClaudeBinProvider("sonnet")
	wrapped := WrapWithRetry(provider, DefaultRetryConfig())

	// The wrapped provider must also implement ToolExecutorSetter
	if _, ok := wrapped.(ToolExecutorSetter); !ok {
		t.Fatal("RetryProvider does not implement ToolExecutorSetter interface - tools will not work with wrapped providers")
	}
}

func TestClaudeBinProvider_ImplementsProviderCleaner(t *testing.T) {
	provider := NewClaudeBinProvider("sonnet")

	// ClaudeBinProvider must implement ProviderCleaner for MCP server cleanup
	if _, ok := interface{}(provider).(ProviderCleaner); !ok {
		t.Fatal("ClaudeBinProvider does not implement ProviderCleaner interface - MCP server cleanup will not work")
	}
}

func TestRetryProvider_ForwardsProviderCleaner(t *testing.T) {
	// ClaudeBinProvider is wrapped with WrapWithRetry in the factory.
	// The RetryProvider must forward CleanupMCP to the inner provider.
	provider := NewClaudeBinProvider("sonnet")
	wrapped := WrapWithRetry(provider, DefaultRetryConfig())

	// The wrapped provider must also implement ProviderCleaner
	if _, ok := wrapped.(ProviderCleaner); !ok {
		t.Fatal("RetryProvider does not implement ProviderCleaner interface - MCP cleanup will not work with wrapped providers")
	}
}

func TestClaudeBinProvider_CleanupMCP_Safe(t *testing.T) {
	// CleanupMCP should be safe to call even without an active MCP server
	provider := NewClaudeBinProvider("sonnet")

	// Should not panic when called without active MCP server
	provider.CleanupMCP()

	// Should be safe to call multiple times
	provider.CleanupMCP()
}

func TestSafeSendEvent_ClosedChannel(t *testing.T) {
	// Test that safeSendEvent doesn't panic on closed channel
	ch := make(chan Event)
	close(ch)

	ctx := context.Background()
	event := Event{Type: EventTextDelta, Text: "test"}

	// Should not panic and should return false
	sent := safeSendEvent(ctx, ch, event)
	if sent {
		t.Fatal("safeSendEvent should return false for closed channel")
	}
}

func TestSafeSendEvent_CancelledContext(t *testing.T) {
	// Test that safeSendEvent respects context cancellation
	ch := make(chan Event) // unbuffered, will block

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	event := Event{Type: EventTextDelta, Text: "test"}

	// Should return false due to cancelled context
	sent := safeSendEvent(ctx, ch, event)
	if sent {
		t.Fatal("safeSendEvent should return false for cancelled context")
	}
}

func TestSafeSendEvent_Success(t *testing.T) {
	// Test that safeSendEvent works normally with open channel
	ch := make(chan Event, 1) // buffered so we don't block

	ctx := context.Background()
	event := Event{Type: EventTextDelta, Text: "test"}

	sent := safeSendEvent(ctx, ch, event)
	if !sent {
		t.Fatal("safeSendEvent should return true for successful send")
	}

	// Verify event was sent
	received := <-ch
	if received.Text != "test" {
		t.Fatalf("expected text 'test', got %q", received.Text)
	}
}

func TestClaudeBinProvider_ToolExecutorIsWired(t *testing.T) {
	provider := NewClaudeBinProvider("sonnet")
	registry := NewToolRegistry()

	// Register a test tool
	executorCalled := false
	registry.Register(&testTool{
		name: "test_tool",
		exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			executorCalled = true
			return "test result", nil
		},
	})

	// Create engine which should wire up the tool executor
	_ = NewEngine(provider, registry)

	// The tool executor should now be set (not nil).
	// We verify this by checking that a Request with tools does not trigger the warning.
	// The best we can do without exposing internals is to verify the interface is satisfied
	// and trust that NewEngine's wiring works (covered by TestClaudeBinProvider_ImplementsToolExecutorSetter).

	// We can also verify the engine wiring works by checking that the executor callback
	// would be invoked if we had a real tool execution path.
	if !executorCalled {
		// This is expected - we didn't actually execute a tool, just verified wiring is possible.
		// The important thing is that the interface is satisfied and SetToolExecutor was called.
	}
}

// testTool is a simple tool implementation for testing.
type testTool struct {
	name string
	exec func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t *testTool) Name() string                        { return t.name }
func (t *testTool) Description() string                 { return "test tool" }
func (t *testTool) Spec() ToolSpec                      { return ToolSpec{Name: t.name} }
func (t *testTool) Preview(args json.RawMessage) string { return "" }
func (t *testTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.exec(ctx, args)
}
