package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
)

type testStream struct {
	events []llm.Event
	index  int
}

func (s *testStream) Recv() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *testStream) Close() error {
	return nil
}

func TestStreamAdapterDedupesToolEvents(t *testing.T) {
	stream := &testStream{
		events: []llm.Event{
			{Type: llm.EventToolExecStart, ToolCallID: "call-1", ToolName: "shell", ToolInfo: "(git status)"},
			{Type: llm.EventToolExecStart, ToolCallID: "call-1", ToolName: "shell", ToolInfo: "(git status)"},
			{Type: llm.EventToolExecEnd, ToolCallID: "call-1", ToolName: "shell", ToolInfo: "(git status)", ToolSuccess: true},
			{Type: llm.EventToolExecEnd, ToolCallID: "call-1", ToolName: "shell", ToolInfo: "(git status)", ToolSuccess: true},
		},
	}

	adapter := NewStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var starts int
	var ends int
	for ev := range adapter.Events() {
		switch ev.Type {
		case StreamEventToolStart:
			starts++
		case StreamEventToolEnd:
			ends++
		}
	}

	if starts != 1 {
		t.Fatalf("expected 1 tool start event, got %d", starts)
	}
	if ends != 1 {
		t.Fatalf("expected 1 tool end event, got %d", ends)
	}
}

func TestStreamAdapterDedupesToolCallAndExecStart(t *testing.T) {
	// Simulate: EventToolCall (during streaming) followed by EventToolExecStart (at execution)
	// Both should result in only ONE ToolStartEvent
	stream := &testStream{
		events: []llm.Event{
			// During streaming, we receive EventToolCall
			{Type: llm.EventToolCall, ToolCallID: "call-1", ToolName: "read_file", Tool: &llm.ToolCall{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: []byte(`{"file_path": "test.txt"}`),
			}},
			// Later, at execution, we receive EventToolExecStart
			{Type: llm.EventToolExecStart, ToolCallID: "call-1", ToolName: "read_file", ToolInfo: "test.txt"},
			// And the end event
			{Type: llm.EventToolExecEnd, ToolCallID: "call-1", ToolName: "read_file", ToolSuccess: true},
		},
	}

	adapter := NewStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var starts int
	var ends int
	for ev := range adapter.Events() {
		switch ev.Type {
		case StreamEventToolStart:
			starts++
		case StreamEventToolEnd:
			ends++
		}
	}

	if starts != 1 {
		t.Fatalf("expected 1 tool start event (deduped EventToolCall + EventToolExecStart), got %d", starts)
	}
	if ends != 1 {
		t.Fatalf("expected 1 tool end event, got %d", ends)
	}
}

func TestParseDiffMarkers(t *testing.T) {
	// Helper to create a valid __DIFF__: marker
	makeDiffMarker := func(file, old, new string, line int) string {
		data := DiffData{File: file, Old: old, New: new, Line: line}
		jsonData, _ := json.Marshal(data)
		return "__DIFF__:" + base64.StdEncoding.EncodeToString(jsonData)
	}

	tests := []struct {
		name     string
		input    string
		expected []DiffData
	}{
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "no markers",
			input:    "some output\nwith multiple lines\nbut no diff markers",
			expected: nil,
		},
		{
			name:  "single diff marker",
			input: makeDiffMarker("test.go", "old line", "new line", 10),
			expected: []DiffData{
				{File: "test.go", Old: "old line", New: "new line", Line: 10},
			},
		},
		{
			name:  "marker with text before",
			input: "Edit applied.\n" + makeDiffMarker("file.py", "x = 1", "x = 2", 5),
			expected: []DiffData{
				{File: "file.py", Old: "x = 1", New: "x = 2", Line: 5},
			},
		},
		{
			name: "multiple markers",
			input: makeDiffMarker("a.go", "a1", "a2", 1) + "\n" +
				makeDiffMarker("b.go", "b1", "b2", 2),
			expected: []DiffData{
				{File: "a.go", Old: "a1", New: "a2", Line: 1},
				{File: "b.go", Old: "b1", New: "b2", Line: 2},
			},
		},
		{
			name:     "invalid base64",
			input:    "__DIFF__:not-valid-base64!!!",
			expected: nil,
		},
		{
			name:     "invalid JSON after decode",
			input:    "__DIFF__:" + base64.StdEncoding.EncodeToString([]byte("not json")),
			expected: nil,
		},
		{
			name:     "empty file field",
			input:    "__DIFF__:" + base64.StdEncoding.EncodeToString([]byte(`{"f":"","o":"x","n":"y","l":1}`)),
			expected: nil,
		},
		{
			name:     "empty marker value",
			input:    "__DIFF__:",
			expected: nil,
		},
		{
			name:     "marker with whitespace",
			input:    "__DIFF__:   " + base64.StdEncoding.EncodeToString([]byte(`{"f":"test.go","o":"a","n":"b","l":1}`)) + "  ",
			expected: []DiffData{{File: "test.go", Old: "a", New: "b", Line: 1}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ParseDiffMarkers(tc.input)
			if len(result) != len(tc.expected) {
				t.Fatalf("expected %d diffs, got %d", len(tc.expected), len(result))
			}
			for i, exp := range tc.expected {
				if result[i].File != exp.File {
					t.Errorf("diff[%d].File = %q, want %q", i, result[i].File, exp.File)
				}
				if result[i].Old != exp.Old {
					t.Errorf("diff[%d].Old = %q, want %q", i, result[i].Old, exp.Old)
				}
				if result[i].New != exp.New {
					t.Errorf("diff[%d].New = %q, want %q", i, result[i].New, exp.New)
				}
				if result[i].Line != exp.Line {
					t.Errorf("diff[%d].Line = %d, want %d", i, result[i].Line, exp.Line)
				}
			}
		})
	}
}
