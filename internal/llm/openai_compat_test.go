package llm

import (
	"testing"
)

func TestSplitParts_WithReasoningContent(t *testing.T) {
	// Test that splitParts correctly extracts reasoning content from parts
	parts := []Part{
		{
			Type:             PartText,
			Text:             "Hello world",
			ReasoningContent: "I need to think about this carefully",
		},
	}

	text, toolCalls, reasoning := splitParts(parts)

	if text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", text)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
	if reasoning != "I need to think about this carefully" {
		t.Errorf("expected reasoning 'I need to think about this carefully', got %q", reasoning)
	}
}

func TestSplitParts_WithToolCallsAndReasoning(t *testing.T) {
	// Test that splitParts handles both tool calls and reasoning
	parts := []Part{
		{
			Type:             PartText,
			Text:             "Let me help you with that",
			ReasoningContent: "The user wants to list files",
		},
		{
			Type: PartToolCall,
			ToolCall: &ToolCall{
				ID:        "call-123",
				Name:      "list_files",
				Arguments: []byte(`{"path": "."}`),
			},
		},
	}

	text, toolCalls, reasoning := splitParts(parts)

	if text != "Let me help you with that" {
		t.Errorf("expected text 'Let me help you with that', got %q", text)
	}
	if len(toolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call-123" {
		t.Errorf("expected tool call ID 'call-123', got %q", toolCalls[0].ID)
	}
	if reasoning != "The user wants to list files" {
		t.Errorf("expected reasoning 'The user wants to list files', got %q", reasoning)
	}
}

func TestBuildCompatMessages_WithReasoningContent(t *testing.T) {
	// Test that buildCompatMessages includes reasoning_content in assistant messages
	messages := []Message{
		{
			Role: RoleUser,
			Parts: []Part{
				{Type: PartText, Text: "What files are here?"},
			},
		},
		{
			Role: RoleAssistant,
			Parts: []Part{
				{
					Type:             PartText,
					Text:             "Let me check",
					ReasoningContent: "User wants to see directory contents",
				},
				{
					Type: PartToolCall,
					ToolCall: &ToolCall{
						ID:        "call-456",
						Name:      "list_files",
						Arguments: []byte(`{"path": "."}`),
					},
				},
			},
		},
	}

	oaiMsgs := buildCompatMessages(messages)

	if len(oaiMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(oaiMsgs))
	}

	// Check user message
	if oaiMsgs[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", oaiMsgs[0].Role)
	}

	// Check assistant message with reasoning
	assistantMsg := oaiMsgs[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected second message role 'assistant', got %q", assistantMsg.Role)
	}
	if assistantMsg.ReasoningContent != "User wants to see directory contents" {
		t.Errorf("expected reasoning_content 'User wants to see directory contents', got %q", assistantMsg.ReasoningContent)
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(assistantMsg.ToolCalls))
	}
}

func TestBuildCompatMessages_NoReasoningContent(t *testing.T) {
	// Test that messages without reasoning_content work correctly
	messages := []Message{
		{
			Role: RoleUser,
			Parts: []Part{
				{Type: PartText, Text: "Hello"},
			},
		},
		{
			Role: RoleAssistant,
			Parts: []Part{
				{Type: PartText, Text: "Hi there!"},
			},
		},
	}

	oaiMsgs := buildCompatMessages(messages)

	if len(oaiMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(oaiMsgs))
	}

	// Verify no reasoning_content is set
	if oaiMsgs[1].ReasoningContent != "" {
		t.Errorf("expected empty reasoning_content, got %q", oaiMsgs[1].ReasoningContent)
	}
}
