package llm

import (
	"encoding/json"
	"testing"
)

func TestToolCallAccumulatorInputJSONDelta(t *testing.T) {
	acc := newToolCallAccumulator()
	start := ToolCall{
		ID:   "tool-1",
		Name: "edit",
	}
	acc.Start(0, start)

	acc.Append(0, `{"file_path":"main.go","old_string":"foo"`)
	acc.Append(0, `,"new_string":"bar"}`)

	final, ok := acc.Finish(0)
	if !ok {
		t.Fatalf("expected tool call")
	}

	var payload map[string]string
	if err := json.Unmarshal(final.Arguments, &payload); err != nil {
		t.Fatalf("failed to unmarshal args: %v", err)
	}

	if payload["file_path"] != "main.go" {
		t.Fatalf("file_path=%q", payload["file_path"])
	}
	if payload["old_string"] != "foo" {
		t.Fatalf("old_string=%q", payload["old_string"])
	}
	if payload["new_string"] != "bar" {
		t.Fatalf("new_string=%q", payload["new_string"])
	}
}

func TestToolCallAccumulatorFallbackArgs(t *testing.T) {
	acc := newToolCallAccumulator()
	start := ToolCall{
		ID:        "tool-2",
		Name:      "edit",
		Arguments: json.RawMessage(`{"file_path":"main.go","old_string":"a","new_string":"b"}`),
	}
	acc.Start(1, start)

	final, ok := acc.Finish(1)
	if !ok {
		t.Fatalf("expected tool call")
	}

	var payload map[string]string
	if err := json.Unmarshal(final.Arguments, &payload); err != nil {
		t.Fatalf("failed to unmarshal args: %v", err)
	}

	if payload["new_string"] != "b" {
		t.Fatalf("new_string=%q", payload["new_string"])
	}
}
