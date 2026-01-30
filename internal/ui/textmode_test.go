package ui

import (
	"strings"
	"testing"
)

func TestTextModeSkipsRenderer(t *testing.T) {
	// Create tracker with TextMode enabled
	tracker := NewToolTracker()
	tracker.TextMode = true

	// Add text segment with width > 0
	tracker.AddTextSegment("**bold** text", 80)

	// Check that no StreamRenderer was created
	if len(tracker.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(tracker.Segments))
	}

	seg := &tracker.Segments[0]
	if seg.StreamRenderer != nil {
		t.Error("StreamRenderer should be nil in TextMode")
	}

	// Complete the segment and check it returns raw text (not rendered)
	tracker.CompleteTextSegments(func(s string) string {
		return s // Just return the input unchanged
	})

	// Get the completed text
	text := seg.GetText()
	if !strings.Contains(text, "**bold**") {
		t.Errorf("expected raw markdown in text mode, got: %s", text)
	}
}

func TestNormalModeUsesRenderer(t *testing.T) {
	// Create tracker without TextMode
	tracker := NewToolTracker()
	// TextMode is false by default

	// Add text segment with width > 0 (enables streaming renderer)
	tracker.AddTextSegment("**bold** text", 80)

	if len(tracker.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(tracker.Segments))
	}

	seg := &tracker.Segments[0]
	if seg.StreamRenderer == nil {
		t.Error("StreamRenderer should be created in normal mode")
	}
}
