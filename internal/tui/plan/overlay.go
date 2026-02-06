package plan

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// hasActiveOverlay returns true if a modal overlay is consuming input.
func (m *Model) hasActiveOverlay() bool {
	return m.helpVisible || m.agentPickerVisible
}

// updateOverlay handles all input when an overlay is active.
// It consumes every message so nothing leaks to the editor/chat.
func (m *Model) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Allow window resize through so the overlay re-renders at the new size.
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return m, nil // already handled by the main Update switch
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		// Swallow mouse, spinner ticks, etc.
		return m, nil
	}

	if m.helpVisible {
		// Any key closes help.
		m.helpVisible = false
		return m, nil
	}

	if m.agentPickerVisible {
		return m.handleAgentPicker(keyMsg)
	}

	return m, nil
}

// handleAgentPicker processes keys when the agent picker is visible.
func (m *Model) handleAgentPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.agentPickerCursor > 0 {
			m.agentPickerCursor--
		}
		return m, nil
	case "down", "j":
		if m.agentPickerCursor < len(m.agentPickerItems)-1 {
			m.agentPickerCursor++
		}
		return m, nil
	case "enter":
		return m.completeHandoff()
	case "esc", "ctrl+c", "q":
		m.agentPickerVisible = false
		m.setStatus("Handoff cancelled")
		return m, nil
	}
	return m, nil
}

// completeHandoff finalizes the handoff with the selected agent.
func (m *Model) completeHandoff() (tea.Model, tea.Cmd) {
	if m.agentPickerCursor == 0 {
		m.handoffAgent = "" // no agent
	} else {
		m.handoffAgent = m.agentPickerItems[m.agentPickerCursor]
	}
	m.agentPickerVisible = false
	m.handedOff = true
	m.quitting = true
	return m, tea.Quit
}

// --- rendering --------------------------------------------------------

// renderAgentPicker renders the agent selection dialog box.
func (m *Model) renderAgentPicker() string {
	theme := m.styles.Theme()

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Primary)
	selectedStyle := lipgloss.NewStyle().Foreground(theme.Primary).Bold(true)
	itemStyle := lipgloss.NewStyle().Foreground(theme.Secondary)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Hand off to agent"))
	b.WriteString("\n\n")

	maxVisible := 15
	startIdx := 0
	if m.agentPickerCursor >= maxVisible {
		startIdx = m.agentPickerCursor - maxVisible + 1
	}
	endIdx := startIdx + maxVisible
	if endIdx > len(m.agentPickerItems) {
		endIdx = len(m.agentPickerItems)
	}

	for i := startIdx; i < endIdx; i++ {
		item := m.agentPickerItems[i]
		if i == m.agentPickerCursor {
			b.WriteString(selectedStyle.Render("❯ " + item))
		} else {
			b.WriteString("  " + itemStyle.Render(item))
		}
		if i < endIdx-1 {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("j/k navigate · enter select · esc cancel"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Border).
		Padding(1, 2)

	return boxStyle.Render(b.String())
}

// renderHelpOverlay renders a centered help box with all keyboard shortcuts.
func (m *Model) renderHelpOverlay() string {
	theme := m.styles.Theme()

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Primary)
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Secondary)
	keyStyle := lipgloss.NewStyle().Foreground(theme.Primary)
	descStyle := lipgloss.NewStyle().Foreground(theme.Muted)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted).Italic(true)

	entry := func(key, desc string) string {
		return "  " + keyStyle.Render(fmt.Sprintf("%-12s", key)) + descStyle.Render(desc)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("Keyboard Shortcuts"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Editor"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+P", "Invoke planner agent"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+S", "Save document"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+G", "Hand off to chat agent"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+A", "Toggle activity panel"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+K", "Clear prompt queue"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+H", "Toggle this help"))
	b.WriteString("\n")
	b.WriteString(entry("Ctrl+C", "Cancel agent / Quit"))
	b.WriteString("\n")
	b.WriteString(entry("Tab", "Switch editor / chat"))
	b.WriteString("\n")
	b.WriteString(entry("Esc", "Normal mode (vim)"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Chat Input"))
	b.WriteString("\n")
	b.WriteString(entry("Enter", "Send instruction"))
	b.WriteString("\n")
	b.WriteString(entry("Esc / Tab", "Return to editor"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Vim Normal Mode"))
	b.WriteString("\n")
	b.WriteString(entry("i / a / o", "Enter insert mode"))
	b.WriteString("\n")
	b.WriteString(entry("dd", "Delete line"))
	b.WriteString("\n")
	b.WriteString(entry("yy", "Yank line"))
	b.WriteString("\n")
	b.WriteString(entry("p", "Paste"))
	b.WriteString("\n")
	b.WriteString(entry("V", "Visual line mode"))
	b.WriteString("\n")
	b.WriteString(entry("gg / G", "Top / bottom"))
	b.WriteString("\n")
	b.WriteString(entry(":w", "Save"))
	b.WriteString("  ")
	b.WriteString(entry(":q", "Quit"))
	b.WriteString("  ")
	b.WriteString(entry(":go", "Handoff"))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(hintStyle.Render("          Press any key to close"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Border).
		Padding(1, 2)

	return boxStyle.Render(b.String())
}

// overlayBox renders a box centered over the given base view.
// It replaces entire rows so styled ANSI content underneath is hidden cleanly.
func (m *Model) overlayBox(base string, overlay string) string {
	overlayLines := strings.Split(overlay, "\n")
	baseLines := strings.Split(base, "\n")

	overlayH := len(overlayLines)
	overlayW := 0
	for _, line := range overlayLines {
		if w := lipgloss.Width(line); w > overlayW {
			overlayW = w
		}
	}

	// Use actual base height so centering is relative to the rendered content.
	baseH := len(baseLines)

	startRow := (baseH - overlayH) / 2
	startCol := (m.width - overlayW) / 2
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}

	// Ensure base has enough rows.
	for len(baseLines) < startRow+overlayH {
		baseLines = append(baseLines, "")
	}

	pad := strings.Repeat(" ", startCol)
	for i, oLine := range overlayLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}
		// Right-pad overlay line so it covers the full overlay width,
		// preventing any background characters from peeking through.
		oVisible := lipgloss.Width(oLine)
		rightPad := ""
		if oVisible < overlayW {
			rightPad = strings.Repeat(" ", overlayW-oVisible)
		}
		baseLines[row] = pad + oLine + rightPad
	}

	return strings.Join(baseLines, "\n")
}
