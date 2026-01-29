package streaming

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
)

func TestFindSafePoint(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected int // expected safe point
	}{
		{
			name:     "plain text",
			content:  "hello world",
			expected: 11,
		},
		{
			name:     "complete bold",
			content:  "hello **world** test",
			expected: 20,
		},
		{
			name:     "incomplete bold",
			content:  "hello **wor",
			expected: 5, // "hello" (trimmed trailing space)
		},
		{
			name:     "incomplete bold with more text",
			content:  "hello **world",
			expected: 5,
		},
		{
			name:     "complete italic",
			content:  "hello *world* test",
			expected: 18,
		},
		{
			name:     "incomplete italic",
			content:  "hello *wor",
			expected: 5,
		},
		{
			name:     "complete code",
			content:  "hello `code` test",
			expected: 17,
		},
		{
			name:     "incomplete code",
			content:  "hello `code",
			expected: 5,
		},
		{
			name:     "incomplete strikethrough",
			content:  "hello ~~strike",
			expected: 5,
		},
		{
			name:     "complete strikethrough",
			content:  "hello ~~strike~~ test",
			expected: 21,
		},
		{
			name:     "incomplete link",
			content:  "hello [link",
			expected: 5,
		},
		{
			name:     "complete link",
			content:  "hello [link](url) test",
			expected: 22,
		},
		{
			name:     "empty content",
			content:  "",
			expected: 0,
		},
		{
			name:     "just asterisks",
			content:  "**",
			expected: 0,
		},
		{
			name:     "escaped asterisk",
			content:  "hello \\*not italic",
			expected: 18,
		},
		{
			name:     "multiple complete markers",
			content:  "**bold** and *italic*",
			expected: 21,
		},
		{
			name:     "nested incomplete",
			content:  "text **bold *italic",
			expected: 4, // "text"
		},
	}

	// Create a minimal renderer for testing
	var buf bytes.Buffer
	sr, err := NewRenderer(&buf, glamour.WithAutoStyle())
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sr.findSafePoint(tt.content)
			if result != tt.expected {
				t.Errorf("findSafePoint(%q) = %d, want %d (safe content: %q)",
					tt.content, result, tt.expected, tt.content[:result])
			}
		})
	}
}

func TestTerminalControllerCountLines(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		width    int
		expected int
	}{
		{
			name:     "single line",
			content:  "hello world",
			width:    80,
			expected: 1,
		},
		{
			name:     "multiple lines",
			content:  "line1\nline2\nline3",
			width:    80,
			expected: 3,
		},
		{
			name:     "wrapped line",
			content:  "this is a very long line that should wrap",
			width:    20,
			expected: 3, // 41 chars at width 20 = 3 lines
		},
		{
			name:     "empty content",
			content:  "",
			width:    80,
			expected: 0,
		},
		{
			name:     "trailing newline",
			content:  "hello\n",
			width:    80,
			expected: 1,
		},
		{
			name:     "only newlines",
			content:  "\n\n\n",
			width:    80,
			expected: 3,
		},
		{
			name:     "with ANSI codes",
			content:  "\x1b[1mhello\x1b[0m",
			width:    80,
			expected: 1, // ANSI codes should not count toward width
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc := newTerminalController(&buf, tt.width)
			result := tc.CountLines(tt.content)
			if result != tt.expected {
				t.Errorf("CountLines(%q, width=%d) = %d, want %d",
					tt.content, tt.width, result, tt.expected)
			}
		})
	}
}

func TestTerminalControllerClearLines(t *testing.T) {
	var buf bytes.Buffer
	tc := newTerminalController(&buf, 80)

	err := tc.ClearLines(3)
	if err != nil {
		t.Fatalf("ClearLines failed: %v", err)
	}

	output := buf.String()

	// Should contain cursor up sequence
	if !strings.Contains(output, "\x1b[3A") {
		t.Errorf("Expected cursor up sequence, got: %q", output)
	}

	// Should contain cursor to column 1
	if !strings.Contains(output, "\x1b[1G") {
		t.Errorf("Expected cursor to column 1 sequence, got: %q", output)
	}

	// Should contain erase display from cursor (\x1b[J or \x1b[0J - both are valid)
	if !strings.Contains(output, "\x1b[J") && !strings.Contains(output, "\x1b[0J") {
		t.Errorf("Expected erase display sequence, got: %q", output)
	}
}

func TestTerminalControllerClearZeroLines(t *testing.T) {
	var buf bytes.Buffer
	tc := newTerminalController(&buf, 80)

	err := tc.ClearLines(0)
	if err != nil {
		t.Fatalf("ClearLines(0) failed: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("ClearLines(0) should not output anything, got: %q", buf.String())
	}
}

func TestNewRendererWithOptions(t *testing.T) {
	var buf bytes.Buffer

	sr, err := NewRendererWithOptions(
		&buf,
		[]StreamRendererOption{
			WithPartialRendering(),
			WithTerminalWidth(120),
		},
		glamour.WithAutoStyle(),
	)
	if err != nil {
		t.Fatalf("NewRendererWithOptions failed: %v", err)
	}

	if !sr.partialEnabled {
		t.Error("partialEnabled should be true")
	}

	if sr.termWidth != 120 {
		t.Errorf("termWidth = %d, want 120", sr.termWidth)
	}

	if sr.termCtrl == nil {
		t.Error("termCtrl should be initialized")
	}
}

func TestNewRendererWithOptionsFlowingMode(t *testing.T) {
	// When partial rendering is enabled without terminal width,
	// it uses flowing mode (no terminal control)
	var buf bytes.Buffer

	sr, err := NewRendererWithOptions(
		&buf,
		[]StreamRendererOption{
			WithPartialRendering(),
		},
		glamour.WithAutoStyle(),
	)
	if err != nil {
		t.Fatalf("NewRendererWithOptions failed: %v", err)
	}

	if sr.termWidth != 0 {
		t.Errorf("termWidth = %d, want 0 (flowing mode)", sr.termWidth)
	}

	if sr.termCtrl != nil {
		t.Error("termCtrl should be nil in flowing mode")
	}

	if !sr.partialEnabled {
		t.Error("partialEnabled should be true")
	}
}

func TestPartialRenderingBackwardsCompatibility(t *testing.T) {
	// Test that the basic NewRenderer still works without partial rendering
	var buf bytes.Buffer

	sr, err := NewRenderer(&buf, glamour.WithAutoStyle())
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}

	if sr.partialEnabled {
		t.Error("partialEnabled should be false by default")
	}

	if sr.termCtrl != nil {
		t.Error("termCtrl should be nil when partial rendering is disabled")
	}

	// Write some content and ensure it works
	_, err = sr.Write([]byte("# Hello World\n\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	err = sr.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should have produced some output
	if buf.Len() == 0 {
		t.Error("Expected some output")
	}
}

func TestCurrentBlockContent(t *testing.T) {
	var buf bytes.Buffer
	sr, err := NewRenderer(&buf, glamour.WithAutoStyle())
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}

	// Simulate partial state
	sr.pendingLines = []string{"line1\n", "line2\n"}
	sr.lineBuf.WriteString("partial")

	content := sr.currentBlockContent()
	expected := "line1\nline2\npartial"
	if content != expected {
		t.Errorf("currentBlockContent() = %q, want %q", content, expected)
	}
}

func TestPartialStateClearing(t *testing.T) {
	var buf bytes.Buffer

	sr, err := NewRendererWithOptions(
		&buf,
		[]StreamRendererOption{
			WithPartialRendering(),
			WithTerminalWidth(80),
		},
		glamour.WithAutoStyle(),
	)
	if err != nil {
		t.Fatalf("NewRendererWithOptions failed: %v", err)
	}

	// Set up some partial state
	sr.partialState = partialState{
		safeMarkdown: "test",
		safeRendered: "test",
		lineCount:    2,
	}

	// Clear the state
	err = sr.clearPartialState()
	if err != nil {
		t.Fatalf("clearPartialState failed: %v", err)
	}

	// State should be reset
	if sr.partialState.safeMarkdown != "" {
		t.Error("safeMarkdown should be empty")
	}
	if sr.partialState.lineCount != 0 {
		t.Error("lineCount should be 0")
	}

	// Should have written clear sequences to buffer
	output := buf.String()
	if !strings.Contains(output, "\x1b[2A") {
		t.Errorf("Expected cursor up sequence for 2 lines, got: %q", output)
	}
}
