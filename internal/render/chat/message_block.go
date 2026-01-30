package chat

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/ui"
)

// MessageBlock represents a pre-rendered, cacheable message.
// Once rendered, blocks are immutable and can be reused across frames.
type MessageBlock struct {
	// MessageID is the unique identifier for this message
	MessageID int64

	// Rendered is the complete rendered output for this message
	Rendered string

	// Height is the number of lines in the rendered output
	Height int

	// Width is the terminal width when this block was rendered
	// (used for cache invalidation on resize)
	Width int
}

// MessageBlockRenderer renders session messages to MessageBlocks.
type MessageBlockRenderer struct {
	width            int
	markdownRenderer MarkdownRenderer
	theme            *ui.Theme
}

// Shared theme instance to avoid allocations
var sharedTheme = ui.DefaultStyles().Theme()

// NewMessageBlockRenderer creates a new renderer for message blocks.
func NewMessageBlockRenderer(width int, mdRenderer MarkdownRenderer) *MessageBlockRenderer {
	return &MessageBlockRenderer{
		width:            width,
		markdownRenderer: mdRenderer,
		theme:            sharedTheme,
	}
}

// Render converts a session.Message to a MessageBlock.
func (r *MessageBlockRenderer) Render(msg *session.Message) *MessageBlock {
	var content string

	switch msg.Role {
	case llm.RoleUser:
		content = r.renderUserMessage(msg)
	case llm.RoleAssistant:
		content = r.renderAssistantMessage(msg)
	default:
		// System messages or other roles - render as plain text
		content = r.renderMarkdown(msg.TextContent)
	}

	return &MessageBlock{
		MessageID: msg.ID,
		Rendered:  content,
		Height:    countLines(content),
		Width:     r.width,
	}
}

// renderUserMessage renders a user message with prompt styling.
func (r *MessageBlockRenderer) renderUserMessage(msg *session.Message) string {
	var b strings.Builder

	promptStyle := lipgloss.NewStyle().
		Foreground(r.theme.Primary).
		Bold(true).
		Background(r.theme.UserMsgBg)
	userMsgStyle := lipgloss.NewStyle().Background(r.theme.UserMsgBg)

	// Extract content before file attachments for display
	displayContent := msg.TextContent
	if idx := strings.Index(displayContent, "\n\n---\n**Attached files:**"); idx != -1 {
		displayContent = strings.TrimSpace(displayContent[:idx])
	}

	// Wrap content to fit terminal width minus prompt
	promptWidth := 2 // "❯ " is 2 cells
	wrapWidth := r.width - promptWidth
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	wrappedContent := wordwrap.String(displayContent, wrapWidth)

	// Render with prompt on first line, indent continuation lines
	lines := strings.Split(wrappedContent, "\n")
	for i, line := range lines {
		if i == 0 {
			b.WriteString(promptStyle.Render("❯ "))
		} else {
			b.WriteString(userMsgStyle.Render("  ")) // 2-space indent for continuation
		}
		b.WriteString(userMsgStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n") // Extra blank line after user messages

	return b.String()
}

// renderAssistantMessage renders an assistant message with all parts.
func (r *MessageBlockRenderer) renderAssistantMessage(msg *session.Message) string {
	var b strings.Builder
	hasContent := false

	for _, part := range msg.Parts {
		switch part.Type {
		case llm.PartText:
			if part.Text != "" {
				rendered := r.renderMarkdown(part.Text)
				b.WriteString(rendered)
				hasContent = true
			}
		case llm.PartToolCall:
			if part.ToolCall != nil {
				b.WriteString(ui.RenderToolCallFromPart(part.ToolCall))
				b.WriteString("\n")
				hasContent = true
			}
			// Skip PartToolResult - they're in user messages and verbose
		}
	}

	// Fallback: if no parts rendered, use TextContent (for backward compatibility)
	if !hasContent && msg.TextContent != "" {
		rendered := r.renderMarkdown(msg.TextContent)
		b.WriteString(rendered)
	}

	b.WriteString("\n\n") // Extra blank line after assistant messages

	return b.String()
}

// renderMarkdown renders markdown content using the configured renderer.
func (r *MessageBlockRenderer) renderMarkdown(content string) string {
	if content == "" {
		return ""
	}
	if r.markdownRenderer == nil {
		return content
	}
	return r.markdownRenderer(content, r.width)
}

// countLines counts the number of newlines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	count := 0
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	// Account for final line without trailing newline
	if len(s) > 0 && s[len(s)-1] != '\n' {
		count++
	}
	return count
}

// RenderEmptyHistory renders the "no messages" placeholder.
func RenderEmptyHistory(theme *ui.Theme) string {
	return lipgloss.NewStyle().
		Foreground(theme.Muted).
		Render("No messages yet. Type your question and press Enter.\n\n")
}

// RenderScrollIndicator renders the scroll position indicator.
func RenderScrollIndicator(offset int, theme *ui.Theme) string {
	if offset == 0 {
		return ""
	}
	scrollInfo := "↑ Scrolled up " + strconv.Itoa(offset) + " message(s) · Press G to go to bottom"
	return lipgloss.NewStyle().
		Foreground(theme.Warning).
		Render(scrollInfo) + "\n\n"
}
