package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SegmentType identifies the type of stream segment
type SegmentType int

const (
	SegmentText SegmentType = iota
	SegmentTool
	SegmentAskUserResult // For ask_user answers (plain text, styled at render time)
)

// ToolStatus represents the execution state of a tool
type ToolStatus int

const (
	ToolPending ToolStatus = iota
	ToolSuccess
	ToolError
)

// Segment represents a discrete unit in the response stream (text or tool)
type Segment struct {
	Type       SegmentType
	Text       string     // For text segments: markdown content
	Rendered   string     // For text segments: cached rendered markdown
	ToolCallID string     // For tool segments: unique ID for this invocation
	ToolName   string     // For tool segments
	ToolInfo   string     // For tool segments: additional context
	ToolStatus ToolStatus // For tool segments
	Complete   bool       // For text segments: whether streaming is complete
}

// Tool status indicator colors using raw ANSI for reliable true color
const (
	pendingCircleANSI = "\033[38;5;245m\u25cb\033[0m"        // gray hollow circle
	workingCircleANSI = "\033[38;2;255;165;0m\u25cf\033[0m"  // orange filled circle for active tools
	successCircleANSI = "\033[38;2;79;185;101m\u25cf\033[0m" // #4FB965 green filled circle
	errorCircleANSI   = "\033[38;2;239;68;68m\u25cf\033[0m"  // #ef4444 red filled circle
)

// PendingCircle returns the pending status indicator
func PendingCircle() string { return pendingCircleANSI }

// WorkingCircle returns the working status indicator
func WorkingCircle() string { return workingCircleANSI }

// SuccessCircle returns the success status indicator
func SuccessCircle() string { return successCircleANSI }

// ErrorCircle returns the error status indicator
func ErrorCircle() string { return errorCircleANSI }

// Wave animation colors
var (
	waveDimColor = lipgloss.Color("245") // dim gray
)

// RenderWaveText renders text with a wave animation effect using bold highlighting.
// wavePos is the position of the bright "peak" traveling through the text.
// If wavePos < 0, we're in the pause phase - show all dim.
func RenderWaveText(text string, wavePos int) string {
	textRunes := []rune(text)
	textLen := len(textRunes)

	dimStyle := lipgloss.NewStyle().Foreground(waveDimColor)
	boldStyle := lipgloss.NewStyle().Bold(true)

	// During pause (wavePos < 0 or >= textLen), show all dim
	if wavePos < 0 || wavePos >= textLen {
		return dimStyle.Render(text)
	}

	// Build string with bold highlight at wave position
	var result strings.Builder

	// Dim text before wave
	if wavePos > 0 {
		result.WriteString(dimStyle.Render(string(textRunes[:wavePos])))
	}

	// Bold highlighted character(s) at wave position - highlight 2 chars for visibility
	highlightEnd := wavePos + 2
	if highlightEnd > textLen {
		highlightEnd = textLen
	}
	result.WriteString(boldStyle.Render(string(textRunes[wavePos:highlightEnd])))

	// Dim text after wave
	if highlightEnd < textLen {
		result.WriteString(dimStyle.Render(string(textRunes[highlightEnd:])))
	}

	return result.String()
}

// Muted style for tool params (lighter than wave dim)
var paramStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))

// RenderToolSegment renders a tool segment with its status indicator.
// For pending tools, wavePos controls the wave animation (-1 = paused/all dim).
// Tool name is rendered normally, params are rendered in slightly muted gray.
func RenderToolSegment(seg *Segment, wavePos int) string {
	switch seg.ToolStatus {
	case ToolPending:
		// Wave animation for pending tools - animate the full text
		phase := FormatToolPhase(seg.ToolName, seg.ToolInfo)
		return PendingCircle() + " " + RenderWaveText(phase.Active, wavePos)
	case ToolSuccess:
		// Tool name normal, params slightly muted (with space before info if present)
		if seg.ToolInfo != "" {
			return SuccessCircle() + " " + seg.ToolName + " " + paramStyle.Render(seg.ToolInfo)
		}
		return SuccessCircle() + " " + seg.ToolName
	case ToolError:
		// Tool name normal, params slightly muted (with space before info if present)
		if seg.ToolInfo != "" {
			return ErrorCircle() + " " + seg.ToolName + " " + paramStyle.Render(seg.ToolInfo)
		}
		return ErrorCircle() + " " + seg.ToolName
	}
	return ""
}

// renderAskUserResult renders an ask_user result with styling applied at render time.
// Input format: "Header: Value" or "Header: Value | Header2: Value2"
func renderAskUserResult(text string) string {
	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)

	var b strings.Builder
	b.WriteString(borderStyle.Render("│") + " ")
	b.WriteString(checkStyle.Render("✓") + " ")

	// Parse "Header: Value | Header2: Value2"
	parts := strings.Split(text, " | ")
	for i, part := range parts {
		if i > 0 {
			b.WriteString(" ")
		}
		if idx := strings.Index(part, ": "); idx != -1 {
			b.WriteString(labelStyle.Render(part[:idx+2]))
			b.WriteString(valueStyle.Render(part[idx+2:]))
		} else {
			b.WriteString(part)
		}
	}
	return b.String() + "\n"
}

// RenderSegments renders a list of segments with proper spacing.
// This is the main entry point for rendering the stream content.
// renderMarkdown should be a function that renders markdown content.
func RenderSegments(segments []Segment, width int, wavePos int, renderMarkdown func(string, int) string) string {
	var b strings.Builder

	for i, seg := range segments {
		if i > 0 {
			prev := segments[i-1]
			prevIsTool := prev.Type == SegmentTool || prev.Type == SegmentAskUserResult
			currIsTool := seg.Type == SegmentTool || seg.Type == SegmentAskUserResult
			// Blank line between text and tools/ask_user results
			if prev.Type == SegmentText && currIsTool {
				b.WriteString("\n\n")
			}
			// Single newline between consecutive tools/ask_user results
			if prevIsTool && currIsTool {
				b.WriteString("\n")
			}
			// Blank line between tools/ask_user results and text
			if prevIsTool && seg.Type == SegmentText {
				b.WriteString("\n\n")
			}
		}

		switch seg.Type {
		case SegmentText:
			if seg.Complete && seg.Rendered != "" {
				b.WriteString(seg.Rendered)
			} else if seg.Text != "" && renderMarkdown != nil {
				b.WriteString(renderMarkdown(seg.Text, width))
			} else if seg.Text != "" {
				b.WriteString(seg.Text)
			}
		case SegmentTool:
			b.WriteString(RenderToolSegment(&seg, wavePos))
		case SegmentAskUserResult:
			b.WriteString(renderAskUserResult(seg.Text))
		}
	}

	return b.String()
}

// HasPendingTool returns true if any segment has a pending tool
func HasPendingTool(segments []Segment) bool {
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i].Type == SegmentTool && segments[i].ToolStatus == ToolPending {
			return true
		}
	}
	return false
}

// GetPendingToolTextLen returns the text length of the first pending tool (for wave animation)
func GetPendingToolTextLen(segments []Segment) int {
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i].Type == SegmentTool && segments[i].ToolStatus == ToolPending {
			phase := FormatToolPhase(segments[i].ToolName, segments[i].ToolInfo)
			return len([]rune(phase.Active))
		}
	}
	return 0
}

// UpdateToolStatus updates the status of a pending tool matching the given call ID.
// Matching by unique ID ensures we update the correct tool when multiple calls
// to the same tool may be in progress.
func UpdateToolStatus(segments []Segment, callID string, success bool) []Segment {
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i].Type == SegmentTool &&
			segments[i].ToolStatus == ToolPending &&
			segments[i].ToolCallID == callID {
			if success {
				segments[i].ToolStatus = ToolSuccess
			} else {
				segments[i].ToolStatus = ToolError
			}
			break
		}
	}
	return segments
}
