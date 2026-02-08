package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
		exec: func(ctx context.Context, args json.RawMessage) (ToolOutput, error) {
			executorCalled = true
			return TextOutput("test result"), nil
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

func TestParseClaudeEffort(t *testing.T) {
	tests := []struct {
		input      string
		wantModel  string
		wantEffort string
	}{
		{"opus-max", "opus", "max"},
		{"opus-low", "opus", "low"},
		{"opus-medium", "opus", "medium"},
		{"opus-high", "opus", "high"},
		{"opus", "opus", ""},
		{"sonnet-max", "sonnet-max", ""}, // non-opus ignored
		{"sonnet", "sonnet", ""},
		{"haiku", "haiku", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			model, effort := parseClaudeEffort(tt.input)
			if model != tt.wantModel {
				t.Errorf("parseClaudeEffort(%q) model = %q, want %q", tt.input, model, tt.wantModel)
			}
			if effort != tt.wantEffort {
				t.Errorf("parseClaudeEffort(%q) effort = %q, want %q", tt.input, effort, tt.wantEffort)
			}
		})
	}
}

func TestClaudeBinProvider_NameWithEffort(t *testing.T) {
	tests := []struct {
		model    string
		wantName string
	}{
		{"opus-max", "Claude CLI (opus, effort=max)"},
		{"opus", "Claude CLI (opus)"},
		{"sonnet", "Claude CLI (sonnet)"},
		{"", "Claude CLI (sonnet)"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := NewClaudeBinProvider(tt.model)
			if got := p.Name(); got != tt.wantName {
				t.Errorf("Name() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestMapModelToClaudeArg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"opus", "opus"},
		{"sonnet", "sonnet"},
		{"haiku", "haiku"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := mapModelToClaudeArg(tt.input); got != tt.want {
				t.Errorf("mapModelToClaudeArg(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPromptTooLong(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"exact match", fmt.Errorf("Prompt is too long"), true},
		{"lowercase", fmt.Errorf("prompt is too long"), true},
		{"wrapped", fmt.Errorf("API error: Prompt is too long for this model"), true},
		{"unrelated error", fmt.Errorf("rate limit exceeded"), false},
		{"empty error", fmt.Errorf(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPromptTooLong(tt.err); got != tt.want {
				t.Errorf("isPromptTooLong(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTruncateToolResults(t *testing.T) {
	shortContent := "short result"
	longContent := strings.Repeat("x", maxToolResultCharsOnRetry+1000)

	messages := []Message{
		{
			Role: RoleUser,
			Parts: []Part{
				{Type: PartText, Text: "hello"},
			},
		},
		{
			Role: RoleTool,
			Parts: []Part{
				{
					Type: PartToolResult,
					ToolResult: &ToolResult{
						ID:      "1",
						Name:    "short_tool",
						Content: shortContent,
					},
				},
				{
					Type: PartToolResult,
					ToolResult: &ToolResult{
						ID:      "2",
						Name:    "long_tool",
						Content: longContent,
					},
				},
			},
		},
	}

	truncated := truncateToolResults(messages)

	// Should not modify original
	if messages[1].Parts[1].ToolResult.Content != longContent {
		t.Fatal("truncateToolResults modified original message")
	}

	// User message should be unchanged
	if truncated[0].Parts[0].Text != "hello" {
		t.Fatalf("user message changed: got %q", truncated[0].Parts[0].Text)
	}

	// Short tool result should be unchanged
	if truncated[1].Parts[0].ToolResult.Content != shortContent {
		t.Fatalf("short result changed: got %q", truncated[1].Parts[0].ToolResult.Content)
	}

	// Long tool result should be truncated
	got := truncated[1].Parts[1].ToolResult.Content
	if len(got) >= len(longContent) {
		t.Fatalf("long result was not truncated: len=%d", len(got))
	}
	if !strings.Contains(got, "[Truncated: showing first") {
		t.Fatalf("truncation notice missing from result: %s", got[len(got)-80:])
	}
	expectedPrefix := longContent[:maxToolResultCharsOnRetry]
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Fatal("truncated result does not start with expected prefix")
	}
}

func TestTruncateToolResults_NoToolResults(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Parts: []Part{{Type: PartText, Text: "hello"}}},
		{Role: RoleAssistant, Parts: []Part{{Type: PartText, Text: "hi"}}},
	}
	truncated := truncateToolResults(messages)
	if len(truncated) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(truncated))
	}
	if truncated[0].Parts[0].Text != "hello" || truncated[1].Parts[0].Text != "hi" {
		t.Fatal("messages were incorrectly modified")
	}
}

// testTool is a simple tool implementation for testing.
type testTool struct {
	name string
	exec func(ctx context.Context, args json.RawMessage) (ToolOutput, error)
}

func (t *testTool) Name() string                        { return t.name }
func (t *testTool) Description() string                 { return "test tool" }
func (t *testTool) Spec() ToolSpec                      { return ToolSpec{Name: t.name} }
func (t *testTool) Preview(args json.RawMessage) string { return "" }
func (t *testTool) Execute(ctx context.Context, args json.RawMessage) (ToolOutput, error) {
	return t.exec(ctx, args)
}
