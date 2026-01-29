package streaming

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// terminalController handles terminal cursor movement and screen clearing
// for partial rendering re-renders.
type terminalController struct {
	output io.Writer
	width  int
}

// newTerminalController creates a new terminal controller.
func newTerminalController(output io.Writer, width int) *terminalController {
	return &terminalController{
		output: output,
		width:  width,
	}
}

// ClearLines moves the cursor up n lines and clears from cursor to end of screen.
// This is used to erase previously rendered partial output before re-rendering.
func (tc *terminalController) ClearLines(n int) error {
	if n <= 0 {
		return nil
	}

	// Move cursor up n lines
	seq := ansi.CursorUp(n)
	// Move to beginning of line
	seq += ansi.CursorHorizontalAbsolute(1)
	// Erase from cursor to end of screen (mode 0)
	seq += ansi.EraseDisplay(0)

	_, err := tc.output.Write([]byte(seq))
	return err
}

// CountLines calculates how many terminal lines the rendered string occupies.
// This accounts for line wrapping based on terminal width and ANSI escape sequences.
func (tc *terminalController) CountLines(rendered string) int {
	if len(rendered) == 0 {
		return 0
	}

	lines := strings.Split(rendered, "\n")
	totalLines := 0

	for i, line := range lines {
		// Don't count the trailing empty string after final newline
		if i == len(lines)-1 && line == "" {
			continue
		}

		// Calculate display width of the line (ignoring ANSI sequences)
		lineWidth := ansi.StringWidth(line)

		if lineWidth == 0 {
			// Empty line still takes one line
			totalLines++
		} else if tc.width > 0 {
			// Calculate how many terminal lines this logical line wraps to
			wrappedLines := (lineWidth + tc.width - 1) / tc.width
			if wrappedLines == 0 {
				wrappedLines = 1
			}
			totalLines += wrappedLines
		} else {
			// No width specified, assume no wrapping
			totalLines++
		}
	}

	return totalLines
}
