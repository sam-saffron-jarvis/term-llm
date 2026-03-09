package ansisafe

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Selection background color: muted blue that works on dark terminals.
// This is similar to the selection color used in VS Code / Sublime Text.
const selBg = "\033[48;2;60;60;100m"

// selBgOff resets only the background to default.
const selBgOff = "\033[49m"

// injectSelectionBg inserts an explicit background color after every SGR
// sequence in s, so that embedded resets (\033[0m) cannot clear the
// selection highlight. The original foreground colors are preserved.
func injectSelectionBg(s string) string {
	if s == "" {
		return ""
	}
	// Fast path: no escape sequences.
	if !strings.Contains(s, "\033[") {
		return selBg + s + selBgOff
	}

	var b strings.Builder
	b.Grow(len(s) + 128)
	b.WriteString(selBg)

	i := 0
	for i < len(s) {
		if s[i] == 0x1B && i+1 < len(s) && s[i+1] == '[' {
			// Scan to CSI terminator (0x40-0x7E).
			j := i + 2
			for j < len(s) && s[j] < 0x40 {
				j++
			}
			if j < len(s) {
				j++ // include terminator
			}
			b.WriteString(s[i:j])
			// Re-assert selection background after any SGR sequence.
			if s[j-1] == 'm' {
				b.WriteString(selBg)
			}
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}

	b.WriteString(selBgOff)
	return b.String()
}

// ApplyReverseVideo applies a selection background highlight to the full line,
// re-asserting the background after any embedded SGR resets so that existing
// foreground colors are preserved.
func ApplyReverseVideo(line string) string {
	if line == "" {
		return selBg + " " + selBgOff
	}
	return injectSelectionBg(line)
}

// ApplyPartialReverseVideo applies a selection background highlight to the
// visual column range [startCol, endCol). endCol == -1 means end-of-line.
// Columns are measured in visible (cell-width) units; ANSI escape sequences
// are transparent. Existing foreground colors are preserved.
func ApplyPartialReverseVideo(line string, startCol, endCol int) string {
	if line == "" {
		return line
	}

	lineWidth := ansi.StringWidth(line)
	if startCol < 0 {
		startCol = 0
	}
	if endCol < 0 || endCol > lineWidth {
		endCol = lineWidth
	}
	if startCol >= endCol {
		return line
	}

	// Full line selection — fast path.
	if startCol == 0 && endCol >= lineWidth {
		return ApplyReverseVideo(line)
	}

	var b strings.Builder
	b.Grow(len(line) + 128)

	// Pre-selection portion.
	if startCol > 0 {
		pre := ansi.Cut(line, 0, startCol)
		b.WriteString(pre)
	}

	// Selected portion with selection background.
	sel := ansi.Cut(line, startCol, endCol)
	b.WriteString(injectSelectionBg(sel))

	// Post-selection portion.
	if endCol < lineWidth {
		post := ansi.Cut(line, endCol, lineWidth)
		b.WriteString(post)
	}

	return b.String()
}
