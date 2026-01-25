package inspector

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/ui"
)

// ContentRenderer handles rendering of conversation messages
type ContentRenderer struct {
	width  int
	styles *ui.Styles
}

// NewContentRenderer creates a new content renderer
func NewContentRenderer(width int, styles *ui.Styles) *ContentRenderer {
	return &ContentRenderer{
		width:  width,
		styles: styles,
	}
}

// RenderMessages renders all messages into a string
func (r *ContentRenderer) RenderMessages(messages []session.Message) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(r.renderMessage(msg))
	}
	return b.String()
}

// renderMessage renders a single message with its parts
func (r *ContentRenderer) renderMessage(msg session.Message) string {
	theme := r.styles.Theme()
	var b strings.Builder

	// Role header
	roleStyle := lipgloss.NewStyle().Bold(true)
	switch msg.Role {
	case llm.RoleUser:
		roleStyle = roleStyle.Foreground(theme.Primary)
		b.WriteString(r.renderBox("User", roleStyle, r.renderUserContent(msg)))
	case llm.RoleAssistant:
		roleStyle = roleStyle.Foreground(theme.Secondary)
		b.WriteString(r.renderBox("Assistant", roleStyle, r.renderAssistantContent(msg)))
	case llm.RoleSystem:
		roleStyle = roleStyle.Foreground(theme.Warning)
		b.WriteString(r.renderBox("System", roleStyle, r.renderTextContent(msg)))
	case llm.RoleTool:
		roleStyle = roleStyle.Foreground(theme.Muted)
		b.WriteString(r.renderToolResults(msg))
	}

	return b.String()
}

// renderBox creates a box around content with a header
func (r *ContentRenderer) renderBox(header string, headerStyle lipgloss.Style, content string) string {
	theme := r.styles.Theme()
	borderColor := theme.Border

	// Calculate inner width (accounting for borders and padding)
	innerWidth := max(r.width-4, 20)

	var b strings.Builder

	// Top border with header
	topLeft := lipgloss.NewStyle().Foreground(borderColor).Render("╭─ ")
	headerText := headerStyle.Render(header)
	headerWidth := lipgloss.Width(topLeft) + lipgloss.Width(headerText) + 1
	remainingDashes := max(r.width-headerWidth-1, 0)
	topRight := lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("─", remainingDashes) + "╮")

	b.WriteString(topLeft)
	b.WriteString(headerText)
	b.WriteString(" ")
	b.WriteString(topRight)
	b.WriteString("\n")

	// Content lines with borders
	lines := strings.Split(content, "\n")
	borderChar := lipgloss.NewStyle().Foreground(borderColor).Render("│")
	for _, line := range lines {
		// Wrap long lines
		wrappedLines := wrapLine(line, innerWidth)
		for _, wl := range wrappedLines {
			padding := max(innerWidth-lipgloss.Width(wl), 0)
			b.WriteString(borderChar)
			b.WriteString(" ")
			b.WriteString(wl)
			b.WriteString(strings.Repeat(" ", padding))
			b.WriteString(" ")
			b.WriteString(borderChar)
			b.WriteString("\n")
		}
	}

	// Bottom border (clamp width to avoid panic on very small terminals)
	borderWidth := max(r.width, 2)
	bottomBorder := lipgloss.NewStyle().Foreground(borderColor).Render("╰" + strings.Repeat("─", borderWidth-2) + "╯")
	b.WriteString(bottomBorder)

	return b.String()
}

// wrapLine wraps a single line to fit within maxWidth
func wrapLine(line string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{line}
	}
	if lipgloss.Width(line) <= maxWidth {
		return []string{line}
	}

	var result []string
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	current := words[0]
	for _, word := range words[1:] {
		test := current + " " + word
		if lipgloss.Width(test) <= maxWidth {
			current = test
		} else {
			result = append(result, current)
			current = word
		}
	}
	result = append(result, current)
	return result
}

// renderUserContent renders user message content
func (r *ContentRenderer) renderUserContent(msg session.Message) string {
	return r.renderTextContent(msg)
}

// renderTextContent extracts and renders text content from a message
func (r *ContentRenderer) renderTextContent(msg session.Message) string {
	var texts []string
	for _, part := range msg.Parts {
		if part.Type == llm.PartText && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) == 0 {
		return "(empty)"
	}
	return strings.Join(texts, "\n")
}

// renderAssistantContent renders assistant message content including tool calls
func (r *ContentRenderer) renderAssistantContent(msg session.Message) string {
	var b strings.Builder
	var hasText bool

	for _, part := range msg.Parts {
		switch part.Type {
		case llm.PartText:
			if part.Text != "" {
				if hasText {
					b.WriteString("\n")
				}
				b.WriteString(part.Text)
				hasText = true
			}
		case llm.PartToolCall:
			if part.ToolCall != nil {
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(r.renderToolCall(part.ToolCall))
			}
		}
	}

	if b.Len() == 0 {
		return "(empty)"
	}
	return b.String()
}

// renderToolCall renders a tool call with its arguments
func (r *ContentRenderer) renderToolCall(tc *llm.ToolCall) string {
	theme := r.styles.Theme()
	var b strings.Builder

	// Tool call header
	headerStyle := lipgloss.NewStyle().Foreground(theme.Secondary).Bold(true)
	idStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	b.WriteString(lipgloss.NewStyle().Foreground(theme.Border).Render("┌ "))
	b.WriteString(headerStyle.Render("Tool Call: "))
	b.WriteString(lipgloss.NewStyle().Foreground(theme.Primary).Render(tc.Name))
	b.WriteString(" ")
	b.WriteString(idStyle.Render("[" + truncateID(tc.ID) + "]"))
	b.WriteString("\n")

	// Arguments as pretty-printed JSON
	args := r.formatJSON(tc.Arguments)
	innerWidth := max(r.width-6, 20)
	borderStyle := lipgloss.NewStyle().Foreground(theme.Border)
	for line := range strings.SplitSeq(args, "\n") {
		// Wrap long lines
		wrappedLines := wrapLine(line, innerWidth)
		for _, wl := range wrappedLines {
			b.WriteString(borderStyle.Render("│ "))
			b.WriteString(wl)
			b.WriteString("\n")
		}
	}

	b.WriteString(lipgloss.NewStyle().Foreground(theme.Border).Render("└"))

	return b.String()
}

// renderToolResults renders tool result messages
func (r *ContentRenderer) renderToolResults(msg session.Message) string {
	theme := r.styles.Theme()
	var b strings.Builder

	for _, part := range msg.Parts {
		if part.Type == llm.PartToolResult && part.ToolResult != nil {
			tr := part.ToolResult

			// Tool result header
			headerStyle := lipgloss.NewStyle().Foreground(theme.Secondary).Bold(true)
			idStyle := lipgloss.NewStyle().Foreground(theme.Muted)

			statusIcon := lipgloss.NewStyle().Foreground(theme.Success).Render("✓")
			if tr.IsError {
				statusIcon = lipgloss.NewStyle().Foreground(theme.Error).Render("✗")
			}

			b.WriteString(lipgloss.NewStyle().Foreground(theme.Border).Render("┌ "))
			b.WriteString(headerStyle.Render("Tool Result: "))
			b.WriteString(lipgloss.NewStyle().Foreground(theme.Primary).Render(tr.Name))
			b.WriteString(" ")
			b.WriteString(idStyle.Render("[" + truncateID(tr.ID) + "]"))
			b.WriteString(" ")
			b.WriteString(statusIcon)
			b.WriteString("\n")

			// Content
			content := tr.Content
			// Limit content display for very long results
			const maxContentLines = 50
			lines := strings.Split(content, "\n")
			if len(lines) > maxContentLines {
				lines = append(lines[:maxContentLines], fmt.Sprintf("... (%d more lines)", len(lines)-maxContentLines))
			}

			innerWidth := max(r.width-6, 20)
			borderStyle := lipgloss.NewStyle().Foreground(theme.Border)
			for _, line := range lines {
				// Wrap long lines instead of truncating
				wrappedLines := wrapLine(line, innerWidth)
				for _, wl := range wrappedLines {
					b.WriteString(borderStyle.Render("│ "))
					b.WriteString(wl)
					b.WriteString("\n")
				}
			}

			// Show total size if truncated
			if len(content) > 1000 {
				b.WriteString(lipgloss.NewStyle().Foreground(theme.Border).Render("│ "))
				b.WriteString(idStyle.Render(fmt.Sprintf("(%d bytes total)", len(content))))
				b.WriteString("\n")
			}

			b.WriteString(lipgloss.NewStyle().Foreground(theme.Border).Render("└"))
			b.WriteString("\n")
		}
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// formatJSON formats JSON with indentation
func (r *ContentRenderer) formatJSON(data json.RawMessage) string {
	if len(data) == 0 {
		return "{}"
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		// If it's not valid JSON, return as-is
		return string(data)
	}

	formatted, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(formatted)
}

// truncateID shortens a tool call ID for display
func truncateID(id string) string {
	if len(id) <= 16 {
		return id
	}
	// Show first 8 and last 4 characters
	return id[:8] + "..." + id[len(id)-4:]
}
