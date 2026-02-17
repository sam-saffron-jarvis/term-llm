package llm

import (
	"encoding/json"
	"testing"
)

func TestToolResultMessageFromOutput_WithDiffs(t *testing.T) {
	output := ToolOutput{
		Content: "Edited test.go: replaced 5 lines with 7 lines.",
		Diffs: []DiffData{
			{File: "test.go", Old: "old code", New: "new code", Line: 1},
		},
	}
	msg := ToolResultMessageFromOutput("call-1", "edit_file", output, nil)

	if len(msg.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(msg.Parts))
	}
	result := msg.Parts[0].ToolResult
	if result == nil {
		t.Fatal("expected ToolResult to be non-nil")
	}

	if result.Content != "Edited test.go: replaced 5 lines with 7 lines." {
		t.Errorf("Content = %q, want clean text", result.Content)
	}
	if len(result.Diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(result.Diffs))
	}
	if result.Diffs[0].File != "test.go" {
		t.Errorf("Diffs[0].File = %q, want %q", result.Diffs[0].File, "test.go")
	}
}

func TestToolResultMessage_PlainText(t *testing.T) {
	raw := "Created new file: /tmp/test.go (10 lines)."
	msg := ToolResultMessage("call-1", "write_file", raw, nil)

	result := msg.Parts[0].ToolResult
	if result.Content != raw {
		t.Errorf("Content = %q, want %q", result.Content, raw)
	}
	if len(result.Diffs) != 0 {
		t.Errorf("expected no diffs, got %d", len(result.Diffs))
	}
	if len(result.Images) != 0 {
		t.Errorf("expected no images, got %d", len(result.Images))
	}
}

func TestToolResultMessageFromOutput_WithImages(t *testing.T) {
	output := ToolOutput{
		Content: "Generated image successfully.",
		Images:  []string{"/tmp/generated.png"},
	}
	msg := ToolResultMessageFromOutput("call-2", "generate_image", output, nil)

	result := msg.Parts[0].ToolResult
	if result.Content != "Generated image successfully." {
		t.Errorf("Content = %q, want clean text", result.Content)
	}
	if len(result.Images) != 1 || result.Images[0] != "/tmp/generated.png" {
		t.Errorf("Images = %v, want [/tmp/generated.png]", result.Images)
	}
}

func TestToolResultMessageFromOutput_WithContentParts(t *testing.T) {
	output := ToolOutput{
		Content: "Image loaded",
		ContentParts: []ToolContentPart{
			{Type: ToolContentPartText, Text: "Image loaded"},
			{
				Type:      ToolContentPartImageData,
				ImageData: &ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="},
			},
		},
	}
	msg := ToolResultMessageFromOutput("call-3", "view_image", output, nil)

	result := msg.Parts[0].ToolResult
	if len(result.ContentParts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(result.ContentParts))
	}
	if result.ContentParts[0].Type != ToolContentPartText {
		t.Fatalf("expected first content part type text, got %q", result.ContentParts[0].Type)
	}
	if result.ContentParts[1].Type != ToolContentPartImageData || result.ContentParts[1].ImageData == nil {
		t.Fatalf("expected second content part type image_data with data, got %#v", result.ContentParts[1])
	}
}

func TestToolResult_SessionRoundTrip(t *testing.T) {
	original := ToolResult{
		ID:      "call-1",
		Name:    "edit_file",
		Content: "Edited test.go: replaced 5 lines with 7 lines.",
		Diffs: []DiffData{
			{File: "test.go", Old: "old", New: "new", Line: 1},
		},
		IsError: false,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var restored ToolResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if restored.Content != original.Content {
		t.Errorf("Content = %q, want %q", restored.Content, original.Content)
	}
	if len(restored.Diffs) != 1 {
		t.Fatalf("Diffs length = %d, want 1", len(restored.Diffs))
	}
	if restored.Diffs[0].File != "test.go" {
		t.Errorf("Diffs[0].File = %q, want %q", restored.Diffs[0].File, "test.go")
	}
	if restored.ID != original.ID {
		t.Errorf("ID = %q, want %q", restored.ID, original.ID)
	}
}

func TestToolResult_SessionRoundTrip_WithContentParts(t *testing.T) {
	original := ToolResult{
		ID:      "call-image",
		Name:    "view_image",
		Content: "Image loaded",
		ContentParts: []ToolContentPart{
			{Type: ToolContentPartText, Text: "Image loaded"},
			{
				Type:      ToolContentPartImageData,
				ImageData: &ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var restored ToolResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(restored.ContentParts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(restored.ContentParts))
	}
	if restored.ContentParts[1].ImageData == nil || restored.ContentParts[1].ImageData.MediaType != "image/png" {
		t.Fatalf("unexpected image data after round trip: %#v", restored.ContentParts[1])
	}
}

func TestToolResult_OldSessionWithDisplay(t *testing.T) {
	// Simulate deserializing an old session that has Display but no Diffs
	jsonData := `{"ID":"call-1","Name":"edit_file","Content":"Edited test.go","Display":"Edited test.go\n__DIFF__:abc123"}`
	var result ToolResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if result.Display != "Edited test.go\n__DIFF__:abc123" {
		t.Errorf("Display = %q, want old marker content preserved", result.Display)
	}
	if len(result.Diffs) != 0 {
		t.Errorf("expected no Diffs for old session, got %d", len(result.Diffs))
	}
}

func TestToolErrorMessage_NoDiffsOrImages(t *testing.T) {
	msg := ToolErrorMessage("call-1", "edit_file", "file not found", nil)

	result := msg.Parts[0].ToolResult
	if result.Content != "file not found" {
		t.Errorf("Content = %q, want %q", result.Content, "file not found")
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if len(result.Diffs) != 0 {
		t.Errorf("expected no diffs for error, got %d", len(result.Diffs))
	}
	if len(result.Images) != 0 {
		t.Errorf("expected no images for error, got %d", len(result.Images))
	}
}

func TestTextOutput(t *testing.T) {
	output := TextOutput("hello world")
	if output.Content != "hello world" {
		t.Errorf("Content = %q, want %q", output.Content, "hello world")
	}
	if len(output.Diffs) != 0 {
		t.Errorf("expected no diffs, got %d", len(output.Diffs))
	}
	if len(output.Images) != 0 {
		t.Errorf("expected no images, got %d", len(output.Images))
	}
}
