package streaming

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/glamour"
)

// TestGlamourParity verifies streaming output exactly matches glamour.
func TestGlamourParity(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple heading", "# Hello\n"},
		{"heading and paragraph", "# Hello\n\nWorld\n"},
		{"two paragraphs", "Hello\n\nWorld\n"},
		{"heading paragraph list", "# Title\n\nParagraph\n\n- Item 1\n- Item 2\n\nDone.\n"},
		{"code block", "```go\nfmt.Println(\"hi\")\n```\n"},
		{"mixed content", "# Heading\n\nThis is a paragraph.\n\n- Item 1\n- Item 2\n\n```\ncode\n```\n\nDone.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Glamour direct render
			tr, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
			if err != nil {
				t.Fatalf("Failed to create glamour renderer: %v", err)
			}
			glamourOut, err := tr.RenderBytes([]byte(tt.input))
			if err != nil {
				t.Fatalf("Glamour render failed: %v", err)
			}

			// Streaming render (all at once)
			var buf bytes.Buffer
			sr, err := NewRenderer(&buf, glamour.WithStandardStyle("dark"))
			if err != nil {
				t.Fatalf("Failed to create streaming renderer: %v", err)
			}
			sr.Write([]byte(tt.input))
			sr.Close()

			if buf.String() != string(glamourOut) {
				t.Errorf("Parity failed\nInput: %q\nGlamour len: %d, newlines: %d\nStreaming len: %d, newlines: %d\nGlamour: %q\nStreaming: %q",
					tt.input,
					len(glamourOut), bytes.Count(glamourOut, []byte("\n")),
					buf.Len(), bytes.Count(buf.Bytes(), []byte("\n")),
					glamourOut,
					buf.String())
			}
		})
	}
}

// TestGlamourParityChunked verifies streaming output matches glamour even when chunked.
func TestGlamourParityChunked(t *testing.T) {
	input := "# Hello\n\nWorld\n"

	// Glamour direct render
	tr, _ := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
	glamourOut, _ := tr.RenderBytes([]byte(input))

	// Streaming render byte-by-byte
	var buf bytes.Buffer
	sr, _ := NewRenderer(&buf, glamour.WithStandardStyle("dark"))
	for i := 0; i < len(input); i++ {
		sr.Write([]byte{input[i]})
	}
	sr.Close()

	if buf.String() != string(glamourOut) {
		t.Errorf("Chunked parity failed\nGlamour: %q\nStreaming: %q", glamourOut, buf.String())
	}
}
