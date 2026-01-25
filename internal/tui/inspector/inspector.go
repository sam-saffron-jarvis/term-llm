package inspector

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/ui"
)

// CloseMsg signals that the inspector should be closed
type CloseMsg struct{}

// Model is the conversation inspector model
type Model struct {
	// Dimensions
	width  int
	height int

	// Content
	messages     []session.Message
	contentLines []string // Pre-rendered content split into lines
	totalLines   int

	// Scroll state
	scrollY int

	// Components
	styles *ui.Styles
	keyMap KeyMap
}

// New creates a new inspector model
func New(messages []session.Message, width, height int, styles *ui.Styles) *Model {
	if styles == nil {
		styles = ui.DefaultStyles()
	}

	m := &Model{
		width:    width,
		height:   height,
		messages: messages,
		styles:   styles,
		keyMap:   DefaultKeyMap(),
	}

	m.renderContent()
	return m
}

// renderContent renders all messages and splits into lines
func (m *Model) renderContent() {
	renderer := NewContentRenderer(m.width-2, m.styles) // -2 for padding
	content := renderer.RenderMessages(m.messages)
	m.contentLines = strings.Split(content, "\n")
	m.totalLines = len(m.contentLines)
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.renderContent()
		// Adjust scroll if needed
		m.clampScroll()
	}

	return m, nil
}

// handleKeyMsg handles keyboard input
func (m *Model) handleKeyMsg(msg tea.KeyMsg) (*Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Quit):
		return m, func() tea.Msg { return CloseMsg{} }

	case key.Matches(msg, m.keyMap.ScrollUp):
		m.scrollY--
		m.clampScroll()

	case key.Matches(msg, m.keyMap.ScrollDown):
		m.scrollY++
		m.clampScroll()

	case key.Matches(msg, m.keyMap.PageUp):
		m.scrollY -= m.viewportHeight()
		m.clampScroll()

	case key.Matches(msg, m.keyMap.PageDown):
		m.scrollY += m.viewportHeight()
		m.clampScroll()

	case key.Matches(msg, m.keyMap.HalfPageUp):
		m.scrollY -= m.viewportHeight() / 2
		m.clampScroll()

	case key.Matches(msg, m.keyMap.HalfPageDown):
		m.scrollY += m.viewportHeight() / 2
		m.clampScroll()

	case key.Matches(msg, m.keyMap.GoToTop):
		m.scrollY = 0

	case key.Matches(msg, m.keyMap.GoToBottom):
		m.scrollY = m.maxScroll()
	}

	return m, nil
}

// viewportHeight returns the available height for content
func (m *Model) viewportHeight() int {
	// Reserve 3 lines for header and footer
	// Clamp to at least 1 to avoid invalid slice bounds on very small terminals
	return max(1, m.height-3)
}

// maxScroll returns the maximum scroll position
func (m *Model) maxScroll() int {
	max := m.totalLines - m.viewportHeight()
	if max < 0 {
		return 0
	}
	return max
}

// clampScroll ensures scroll is within bounds
func (m *Model) clampScroll() {
	if m.scrollY < 0 {
		m.scrollY = 0
	}
	max := m.maxScroll()
	if m.scrollY > max {
		m.scrollY = max
	}
}

// View renders the model
func (m *Model) View() string {
	theme := m.styles.Theme()
	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.Text).
		Background(theme.Border).
		Padding(0, 1).
		Width(m.width)

	title := "Conversation Inspector"
	msgCount := len(m.messages)
	if msgCount == 1 {
		title += " (1 message)"
	} else {
		title += fmt.Sprintf(" (%d messages)", msgCount)
	}

	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n")

	// Content viewport
	vpHeight := m.viewportHeight()
	endIdx := m.scrollY + vpHeight
	if endIdx > m.totalLines {
		endIdx = m.totalLines
	}

	// Defensive bounds checking for very small terminals or empty content
	startIdx := m.scrollY
	if startIdx > m.totalLines {
		startIdx = m.totalLines
	}
	if startIdx > endIdx {
		startIdx = endIdx
	}

	visibleLines := m.contentLines[startIdx:endIdx]
	content := strings.Join(visibleLines, "\n")

	// Pad content to fill viewport
	lineCount := len(visibleLines)
	if lineCount < vpHeight {
		content += strings.Repeat("\n", vpHeight-lineCount)
	}

	b.WriteString(content)
	b.WriteString("\n")

	// Footer with scroll info and help
	footerStyle := lipgloss.NewStyle().
		Foreground(theme.Muted).
		Width(m.width)

	// Scroll indicator
	scrollInfo := ""
	if m.totalLines > vpHeight {
		pct := 0
		if m.maxScroll() > 0 {
			pct = (m.scrollY * 100) / m.maxScroll()
		}
		scrollInfo = fmt.Sprintf("%d-%d/%d (%d%%)", m.scrollY+1, endIdx, m.totalLines, pct)
	}

	// Help text
	helpStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	help := helpStyle.Render("q:close  j/k:scroll  g/G:top/bottom")

	// Combine footer
	padding := m.width - lipgloss.Width(scrollInfo) - lipgloss.Width(help)
	if padding < 1 {
		padding = 1
	}
	footer := scrollInfo + strings.Repeat(" ", padding) + help

	b.WriteString(footerStyle.Render(footer))

	return b.String()
}
