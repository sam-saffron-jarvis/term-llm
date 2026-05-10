package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeToolHistory_ReusesValidHistory(t *testing.T) {
	toolCall := &ToolCall{
		ID:         "call-1",
		Name:       "read_file",
		Arguments:  json.RawMessage(`{"path":"main.go"}`),
		ThoughtSig: []byte("call-thought"),
	}
	toolResult := &ToolResult{
		ID:      "call-1",
		Name:    "read_file",
		Content: "package main",
		ContentParts: []ToolContentPart{
			{Type: ToolContentPartText, Text: "package main"},
			{Type: ToolContentPartImageData, ImageData: &ToolImageData{MediaType: "image/png", Base64: "abc"}},
		},
		Diffs:      []DiffData{{File: "main.go", Old: "old", New: "new", Line: 1}},
		Images:     []string{"/tmp/main.png"},
		ThoughtSig: []byte("result-thought"),
	}

	messages := []Message{
		{Role: RoleUser, Parts: []Part{{Type: PartText, Text: "Inspect main.go"}}},
		{Role: RoleAssistant, Parts: []Part{{Type: PartText, Text: "Checking"}, {Type: PartToolCall, ToolCall: toolCall}}},
		{Role: RoleTool, Parts: []Part{{Type: PartToolResult, ToolResult: toolResult}}},
		{Role: RoleAssistant, Parts: []Part{{Type: PartText, Text: "Done"}}},
	}

	sanitized := sanitizeToolHistory(messages)
	if len(sanitized) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(sanitized))
	}

	if sanitized[1].Parts[1].ToolCall != toolCall {
		t.Fatal("expected valid tool call to be reused without cloning")
	}
	if sanitized[2].Parts[0].ToolResult != toolResult {
		t.Fatal("expected valid tool result to be reused without cloning")
	}
	if sanitized[2].Parts[0].ToolResult.ContentParts[1].ImageData != toolResult.ContentParts[1].ImageData {
		t.Fatal("expected nested tool result image data to be reused without cloning")
	}
}

func TestSanitizeToolHistory_RepairsOrphanedToolCalls(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Parts: []Part{{Type: PartText, Text: "Run a tool"}}},
		{
			Role: RoleAssistant,
			Parts: []Part{
				{Type: PartText, Text: "Working on it"},
				{
					Type: PartToolCall,
					ToolCall: &ToolCall{
						ID:        "call-1",
						Name:      "shell",
						Arguments: json.RawMessage(`{"command":"sleep 10"}`),
					},
				},
			},
		},
		{Role: RoleUser, Parts: []Part{{Type: PartText, Text: "new request"}}},
	}

	sanitized := sanitizeToolHistory(messages)
	if len(sanitized) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(sanitized))
	}

	assistant := sanitized[1]
	if len(assistant.Parts) != 2 {
		t.Fatalf("expected assistant message to contain preserved text and interruption stub, got %d parts", len(assistant.Parts))
	}
	if assistant.Parts[1].Type != PartText {
		t.Fatalf("expected orphaned tool call to become text, got %q", assistant.Parts[1].Type)
	}
	if !strings.Contains(assistant.Parts[1].Text, "[tool call interrupted") {
		t.Fatalf("expected interruption stub, got %q", assistant.Parts[1].Text)
	}
	if messages[1].Parts[1].ToolCall == nil {
		t.Fatal("expected original message to remain unchanged")
	}
}
