package ui

import (
	"strings"
	"testing"
)

func TestActualCombinedOutput(t *testing.T) {
	md := `## Headers at All Levels

# Header 1

## Header 2

### Header 3

#### Header 4

##### Header 5

###### Header 6`

	tracker := NewToolTracker()

	// Simulate streaming with chunks
	chunkSize := 20
	var allPrinted strings.Builder

	for i := 0; i < len(md); i += chunkSize {
		end := i + chunkSize
		if end > len(md) {
			end = len(md)
		}
		chunk := md[i:end]
		tracker.AddTextSegment(chunk, 80)

		result := tracker.FlushStreamingText(100, 80, RenderMarkdown)
		if result.ToPrint != "" {
			// Simulate tea.Printf adding a newline after each flush
			allPrinted.WriteString(result.ToPrint)
			allPrinted.WriteString("\n") // This is what tea.Printf adds
		}
	}

	// Complete and final flush
	tracker.CompleteTextSegments(func(text string) string {
		return RenderMarkdown(text, 80)
	})

	result := tracker.FlushAllRemaining(80, 0, RenderMarkdown)
	if result.ToPrint != "" {
		allPrinted.WriteString(result.ToPrint)
		allPrinted.WriteString("\n") // tea.Printf adds this too
	}

	// Count lines in combined output
	stripped := stripAnsi(allPrinted.String())
	lines := strings.Split(strings.TrimRight(stripped, "\n"), "\n")

	t.Logf("Total combined output lines: %d", len(lines))

	// Count actual content lines (headers) vs blank lines
	contentLines := 0
	blankLines := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blankLines++
			t.Logf("Line %d: (blank)", i)
		} else {
			contentLines++
			t.Logf("Line %d: %q", i, trimmed)
		}
	}

	t.Logf("Content lines: %d, Blank lines: %d", contentLines, blankLines)

	// We expect 7 headers + 7 blank lines (one before each header) = 14 lines
	// But we might have 1 extra newline at the end from the final Printf
	if len(lines) < 13 || len(lines) > 15 {
		t.Errorf("Expected 13-15 lines, got %d", len(lines))
	}
}
