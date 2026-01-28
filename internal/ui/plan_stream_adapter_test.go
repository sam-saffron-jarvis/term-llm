package ui

import (
	"context"
	"io"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
)

// mockStream implements llm.Stream for testing
type mockStream struct {
	events []llm.Event
	idx    int
}

func (s *mockStream) Recv() (llm.Event, error) {
	if s.idx >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.idx]
	s.idx++
	return event, nil
}

func (s *mockStream) Close() error {
	return nil
}

func TestPlanStreamAdapter_InlineInsert(t *testing.T) {
	stream := &mockStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Text: `<INSERT after="anchor">new line</INSERT>`},
		},
	}

	adapter := NewPlanStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var gotInlineInsert bool
	var gotAfter string
	var gotContent []string

	for event := range adapter.Events() {
		switch event.Type {
		case StreamEventInlineInsert:
			gotInlineInsert = true
			gotAfter = event.InlineAfter
			gotContent = event.InlineContent
		}
	}

	if !gotInlineInsert {
		t.Error("expected inline insert event")
	}
	if gotAfter != "anchor" {
		t.Errorf("after = %q, want %q", gotAfter, "anchor")
	}
	if len(gotContent) != 1 || gotContent[0] != "new line" {
		t.Errorf("content = %v, want [new line]", gotContent)
	}
}

func TestPlanStreamAdapter_InlineDelete(t *testing.T) {
	stream := &mockStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Text: `<DELETE from="line to remove" />`},
		},
	}

	adapter := NewPlanStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var gotInlineDelete bool
	var gotFrom string

	for event := range adapter.Events() {
		switch event.Type {
		case StreamEventInlineDelete:
			gotInlineDelete = true
			gotFrom = event.InlineFrom
		}
	}

	if !gotInlineDelete {
		t.Error("expected inline delete event")
	}
	if gotFrom != "line to remove" {
		t.Errorf("from = %q, want %q", gotFrom, "line to remove")
	}
}

func TestPlanStreamAdapter_StreamedChunks(t *testing.T) {
	// Simulate text being streamed in chunks
	stream := &mockStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Text: `<INS`},
			{Type: llm.EventTextDelta, Text: `ERT after="`},
			{Type: llm.EventTextDelta, Text: `test">`},
			{Type: llm.EventTextDelta, Text: `content`},
			{Type: llm.EventTextDelta, Text: `</INSERT>`},
		},
	}

	adapter := NewPlanStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var gotInlineInsert bool
	var gotAfter string
	var gotContent []string

	for event := range adapter.Events() {
		switch event.Type {
		case StreamEventInlineInsert:
			gotInlineInsert = true
			gotAfter = event.InlineAfter
			gotContent = event.InlineContent
		}
	}

	if !gotInlineInsert {
		t.Error("expected inline insert event")
	}
	if gotAfter != "test" {
		t.Errorf("after = %q, want %q", gotAfter, "test")
	}
	if len(gotContent) != 1 || gotContent[0] != "content" {
		t.Errorf("content = %v, want [content]", gotContent)
	}
}

func TestPlanStreamAdapter_MultipleEdits(t *testing.T) {
	stream := &mockStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Text: `<INSERT after="first">line 1</INSERT>`},
			{Type: llm.EventTextDelta, Text: `<DELETE from="old" />`},
			{Type: llm.EventTextDelta, Text: `<INSERT>line 2</INSERT>`},
		},
	}

	adapter := NewPlanStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var insertCount, deleteCount int

	for event := range adapter.Events() {
		switch event.Type {
		case StreamEventInlineInsert:
			insertCount++
		case StreamEventInlineDelete:
			deleteCount++
		}
	}

	if insertCount != 2 {
		t.Errorf("insert count = %d, want 2", insertCount)
	}
	if deleteCount != 1 {
		t.Errorf("delete count = %d, want 1", deleteCount)
	}
}

func TestPlanStreamAdapter_TextPassthrough(t *testing.T) {
	stream := &mockStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Text: "Before "},
			{Type: llm.EventTextDelta, Text: `<INSERT>content</INSERT>`},
			{Type: llm.EventTextDelta, Text: " After"},
		},
	}

	adapter := NewPlanStreamAdapter(10)
	go adapter.ProcessStream(context.Background(), stream)

	var textParts []string
	var gotInsert bool

	for event := range adapter.Events() {
		switch event.Type {
		case StreamEventText:
			textParts = append(textParts, event.Text)
		case StreamEventInlineInsert:
			gotInsert = true
		}
	}

	if !gotInsert {
		t.Error("expected inline insert event")
	}

	// Check that non-marker text was passed through
	combinedText := ""
	for _, part := range textParts {
		combinedText += part
	}
	if combinedText != "Before  After" {
		t.Errorf("text = %q, want %q", combinedText, "Before  After")
	}
}
