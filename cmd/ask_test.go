package cmd

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/testutil"
	"github.com/samsaffron/term-llm/internal/ui"
)

// Test content similar to the Ruby release notes
const testMarkdown = `Great news! Ruby 4.0 was just released on December 25th, 2025 (Christmas Day) - marking 30 years since Ruby's first public release! Here are some of the coolest features:

## **ZJIT - New JIT Compiler**
ZJIT is a new just-in-time (JIT) compiler, which is developed as the next generation of YJIT. Unlike YJIT's lazy basic block versioning approach, ZJIT uses a more traditional method based compilation strategy, is designed to be more accessible to contributors, and follows a "textbook" compiler architecture that's easier to understand and modify. While ZJIT is faster than the interpreter, but not yet as fast as YJIT, it sets the foundation for future performance improvements and easier community contributions.

## **Ruby Box - Experimental Isolation Feature**
Ruby Box is a new (experimental) feature to provide separation about definitions. Ruby Box can isolate/separate monkey patches, changes of global/class variables, class/module definitions, and loaded native/ruby libraries from other boxes. This means you can load multiple versions of a library simultaneously and isolate test cases from each other!

## **Ractor Improvements**
Ractor, Ruby's parallel execution mechanism, has received several improvements, including a new class, Ractor::Port, which was introduced to address issues related to message sending and receiving.

## **Language Changes**
Some nice syntax improvements:
- Logical binary operators (||, &&, and and or) at the beginning of a line continue the previous line, like fluent dot.
- Set has been promoted from stdlib to a core class. No more ` + "`require 'set'`" + ` needed!

It's an exciting release that balances new experimental features with practical improvements!`

func TestMarkdownRendering(t *testing.T) {
	width := 80

	// Test full render
	fullRender, err := ui.RenderMarkdownWithError(testMarkdown, width)
	if err != nil {
		t.Fatalf("Failed to render full markdown: %v", err)
	}

	t.Logf("Full render output:\n%s", fullRender)
	t.Logf("Full render length: %d chars, %d lines", len(fullRender), strings.Count(fullRender, "\n"))
}

func TestIncrementalRendering(t *testing.T) {
	width := 80

	// Simulate streaming by splitting content and rendering incrementally
	// This mimics what streamWithGlamour does
	chunks := simulateStreaming(testMarkdown)

	var content strings.Builder
	var printedLines int
	var allOutput strings.Builder

	for i, chunk := range chunks {
		content.WriteString(chunk)

		// Only render when we get a newline (like the real code does)
		if strings.Contains(chunk, "\n") {
			rendered, err := ui.RenderMarkdownWithError(content.String(), width)
			if err != nil {
				t.Fatalf("Render failed at chunk %d: %v", i, err)
			}

			lines := strings.Split(rendered, "\n")
			for j := printedLines; j < len(lines); j++ {
				if j < len(lines)-1 {
					allOutput.WriteString(lines[j])
					allOutput.WriteString("\n")
					printedLines++
				}
			}
		}
	}

	// Final render
	finalRendered, _ := ui.RenderMarkdownWithError(content.String(), width)
	lines := strings.Split(finalRendered, "\n")
	for i := printedLines; i < len(lines); i++ {
		line := lines[i]
		if line != "" || i < len(lines)-1 {
			allOutput.WriteString(line)
			allOutput.WriteString("\n")
		}
	}

	// Compare with full render
	fullRender, _ := ui.RenderMarkdownWithError(testMarkdown, width)

	t.Logf("Incremental output:\n%s", allOutput.String())
	t.Logf("Full render:\n%s", fullRender)

	// Allow small line count difference (streaming may add/remove trailing newline)
	incrementalLines := strings.Count(allOutput.String(), "\n")
	fullLines := strings.Count(fullRender, "\n")
	lineDiff := incrementalLines - fullLines
	if lineDiff < 0 {
		lineDiff = -lineDiff
	}

	if lineDiff > 1 {
		t.Errorf("Line count differs significantly!\nIncremental lines: %d\nFull lines: %d",
			incrementalLines, fullLines)
	}

	// Ensure ANSI escape codes are preserved (the main fix we're testing)
	if !strings.Contains(allOutput.String(), "\x1b[") {
		t.Error("Incremental output missing ANSI escape codes - wordwrap may be breaking styling")
	}
	if !strings.Contains(fullRender, "\x1b[") {
		t.Error("Full render missing ANSI escape codes")
	}
}

func TestAskDoneRendersMarkdown(t *testing.T) {
	model := newAskStreamModel()
	model.width = 80

	updated, _ := model.Update(askContentMsg("**bold**"))
	model = updated.(askStreamModel)

	updated, _ = model.Update(askDoneMsg{})
	model = updated.(askStreamModel)

	// Final output is stored in finalOutput (printed after p.Run() completes)
	// View() returns empty to avoid duplicate rendering
	output := model.finalOutput
	if strings.Contains(output, "**") {
		t.Fatalf("expected markdown to be rendered on completion, got raw output: %q", output)
	}
	if !strings.Contains(output, "bold") {
		t.Fatalf("expected rendered output to contain content, got: %q", output)
	}
}

func TestAskDoneDoesNotFlushSegments(t *testing.T) {
	model := newAskStreamModel()
	model.width = 80

	updated, _ := model.Update(askToolStartMsg{CallID: "call-1", Name: "shell", Info: "(git status)"})
	model = updated.(askStreamModel)

	updated, _ = model.Update(askToolEndMsg{CallID: "call-1", Success: true})
	model = updated.(askStreamModel)

	updated, _ = model.Update(askDoneMsg{})
	model = updated.(askStreamModel)

	if len(model.tracker.Segments) == 0 {
		t.Fatal("expected tool segment to be tracked")
	}
	if model.tracker.Segments[0].Flushed {
		t.Fatalf("expected segments to remain unflushed on done to avoid duplicate scrollback output")
	}
}

// TestAskDoneFlushedContentNotInFinalOutput verifies that flushed content does NOT
// appear in finalOutput. Flushed content is already in scrollback via
// tea.Println(), so including it in finalOutput would cause double rendering.
func TestAskDoneFlushedContentNotInFinalOutput(t *testing.T) {
	model := newAskStreamModel()
	model.width = 80

	// Add content and simulate it being flushed to scrollback
	updated, _ := model.Update(askContentMsg("First paragraph."))
	model = updated.(askStreamModel)
	model.tracker.Segments[0].Complete = true
	model.tracker.Segments[0].Flushed = true // Simulate flushed to scrollback

	// Add more content (unflushed)
	updated, _ = model.Update(askContentMsg("Second paragraph."))
	model = updated.(askStreamModel)

	// Complete
	updated, _ = model.Update(askDoneMsg{})
	model = updated.(askStreamModel)

	output := model.finalOutput

	// Flushed content should NOT appear in final output (it's in scrollback)
	if strings.Contains(output, "First") {
		t.Errorf("Final output should NOT contain flushed content 'First', got: %q", output)
	}
	// Unflushed content should appear
	if !strings.Contains(output, "Second") {
		t.Errorf("Final output should contain unflushed content 'Second', got: %q", output)
	}
}

// TestAskDoneNoDoubleRendering verifies that content appears exactly once
// in the finalOutput, preventing double rendering issues.
func TestAskDoneNoDoubleRendering(t *testing.T) {
	model := newAskStreamModel()
	model.width = 80

	// Add unique content
	updated, _ := model.Update(askContentMsg("UNIQUE_MARKER_12345"))
	model = updated.(askStreamModel)

	// Complete
	updated, _ = model.Update(askDoneMsg{})
	model = updated.(askStreamModel)

	output := model.finalOutput

	// Strip ANSI codes before counting (markdown renderer adds escape codes)
	plainOutput := testutil.StripANSI(output)

	// Count occurrences - should be exactly 1
	count := strings.Count(plainOutput, "UNIQUE_MARKER_12345")
	if count != 1 {
		t.Errorf("Content should appear exactly once, got %d times in: %q", count, plainOutput)
	}
}

// TestToolSegmentFlushedNotInFinalOutput verifies that flushed tool segments do NOT
// appear in finalOutput. They're already in scrollback via tea.Println().
func TestToolSegmentFlushedNotInFinalOutput(t *testing.T) {
	model := newAskStreamModel()
	model.width = 80

	// Simulate: text -> tool (flushed) -> text
	updated, _ := model.Update(askContentMsg("Before tool."))
	model = updated.(askStreamModel)
	model.tracker.Segments[0].Complete = true

	updated, _ = model.Update(askToolStartMsg{CallID: "c1", Name: "shell", Info: "(pwd)"})
	model = updated.(askStreamModel)

	updated, _ = model.Update(askToolEndMsg{CallID: "c1", Success: true})
	model = updated.(askStreamModel)

	// Simulate the text and tool getting flushed to scrollback
	model.tracker.Segments[0].Flushed = true
	model.tracker.Segments[1].Flushed = true

	updated, _ = model.Update(askContentMsg("After tool."))
	model = updated.(askStreamModel)

	updated, _ = model.Update(askDoneMsg{})
	model = updated.(askStreamModel)

	output := model.finalOutput

	// Flushed segments should NOT appear in final output
	if strings.Contains(output, "Before") {
		t.Errorf("Flushed 'Before' should NOT be in finalOutput: %s", output)
	}
	if strings.Contains(output, "shell") {
		t.Errorf("Flushed tool 'shell' should NOT be in finalOutput: %s", output)
	}
	// Only unflushed content should appear
	if !strings.Contains(output, "After") {
		t.Errorf("Unflushed 'After' should be in finalOutput: %s", output)
	}
}

// simulateStreaming splits content into chunks like an LLM would stream it
func simulateStreaming(content string) []string {
	var chunks []string
	words := strings.Fields(content)

	for i, word := range words {
		if i > 0 {
			chunks = append(chunks, " ")
		}
		chunks = append(chunks, word)

		// Add newlines where they appear in original
		idx := strings.Index(content, word)
		if idx >= 0 {
			afterWord := idx + len(word)
			if afterWord < len(content) && content[afterWord] == '\n' {
				chunks = append(chunks, "\n")
				if afterWord+1 < len(content) && content[afterWord+1] == '\n' {
					chunks = append(chunks, "\n")
				}
			}
		}
	}

	return chunks
}
