package plan

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleVimNormalMode processes keys in vim normal mode.
func (m *Model) handleVimNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Handle visual mode keys first
	if m.visualMode {
		return m.handleVisualMode(key)
	}

	// Handle pending multi-key commands
	if m.vimPending != "" {
		return m.handleVimPending(key)
	}

	switch key {
	// Mode switching
	case "i":
		m.vimMode = false
		return m, nil
	case "a":
		m.vimMode = false
		// Move cursor right (after current char)
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyRight})
		return m, nil
	case "o":
		m.vimMode = false
		m.vimInsertLineBelow()
		return m, nil
	case "O":
		m.vimMode = false
		m.vimInsertLineAbove()
		return m, nil

	// Navigation
	case "h":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyLeft})
		return m, nil
	case "j":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
		return m, nil
	case "k":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyUp})
		return m, nil
	case "l":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyRight})
		return m, nil

	// Word navigation
	case "w":
		m.vimWordForward()
		return m, nil
	case "b":
		m.vimWordBackward()
		return m, nil

	// Line navigation
	case "0":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
		return m, nil
	case "$":
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyEnd})
		return m, nil

	// Document navigation (multi-key)
	case "g":
		m.vimPending = "g"
		return m, nil
	case "G":
		m.vimGotoEnd()
		return m, nil

	// Editing
	case "x":
		m.vimDeleteChar()
		m.syncDocFromEditor()
		return m, nil
	case "d":
		m.vimPending = "d"
		return m, nil
	case "y":
		m.vimPending = "y"
		return m, nil
	case "p":
		m.vimPaste()
		m.syncDocFromEditor()
		return m, nil

	// Visual mode
	case "V":
		m.vimEnterVisualMode()
		return m, nil

	// Command mode
	case ":":
		m.commandMode = true
		m.commandBuffer = ""
		return m, nil
	}

	return m, nil
}

// handleVimPending handles multi-key commands like dd, yy, gg.
func (m *Model) handleVimPending(key string) (tea.Model, tea.Cmd) {
	pending := m.vimPending
	m.vimPending = ""

	switch pending {
	case "g":
		if key == "g" {
			m.vimGotoStart()
		}
	case "d":
		if key == "d" {
			m.vimDeleteLine()
			m.syncDocFromEditor()
		}
	case "y":
		if key == "y" {
			m.vimYankLine()
		}
	}

	return m, nil
}

// handleVisualMode processes keys in visual line mode.
func (m *Model) handleVisualMode(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.visualMode = false
		return m, nil
	case "j":
		// Extend selection down
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.visualEnd = m.editor.Line()
		return m, nil
	case "k":
		// Extend selection up
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyUp})
		m.visualEnd = m.editor.Line()
		return m, nil
	case "d":
		// Delete selected lines
		m.vimDeleteSelection()
		m.syncDocFromEditor()
		return m, nil
	case "y":
		// Yank selected lines
		m.vimYankSelection()
		return m, nil
	case "G":
		// Extend to end of document
		m.vimGotoEnd()
		m.visualEnd = m.editor.Line()
		return m, nil
	case "g":
		m.vimPending = "g"
		return m, nil
	}

	// Handle pending g command in visual mode
	if m.vimPending == "g" {
		m.vimPending = ""
		if key == "g" {
			m.vimGotoStart()
			m.visualEnd = m.editor.Line()
		}
		return m, nil
	}

	return m, nil
}

// handleCommandMode processes keys in command mode (after pressing :).
func (m *Model) handleCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// Execute the command
		return m.executeCommand()
	case tea.KeyEsc:
		// Cancel command mode
		m.commandMode = false
		m.commandBuffer = ""
		return m, nil
	case tea.KeyBackspace:
		// Delete last character
		if len(m.commandBuffer) > 0 {
			m.commandBuffer = m.commandBuffer[:len(m.commandBuffer)-1]
		}
		if len(m.commandBuffer) == 0 {
			// Exit command mode if buffer is empty
			m.commandMode = false
		}
		return m, nil
	default:
		// Add character to buffer
		key := msg.String()
		if len(key) == 1 {
			m.commandBuffer += key
		}
		return m, nil
	}
}

// executeCommand executes the command in the command buffer.
func (m *Model) executeCommand() (tea.Model, tea.Cmd) {
	cmd := strings.TrimSpace(m.commandBuffer)
	m.commandMode = false
	m.commandBuffer = ""

	switch cmd {
	case "w":
		// Save
		return m.saveDocument()
	case "wq", "x":
		// Save and quit
		_, _ = m.saveDocument()
		m.quitting = true
		return m, tea.Quit
	case "q":
		// Quit (without saving)
		m.quitting = true
		return m, tea.Quit
	case "q!":
		// Force quit
		m.quitting = true
		return m, tea.Quit
	case "go":
		// Hand off to chat agent
		return m.handoff()
	default:
		m.setStatus(fmt.Sprintf("Unknown command: %s", cmd))
		return m, nil
	}
}

// vimEnterVisualMode enters visual line mode.
func (m *Model) vimEnterVisualMode() {
	m.visualMode = true
	m.visualStart = m.editor.Line()
	m.visualEnd = m.visualStart
}

// vimDeleteSelection deletes the selected lines in visual mode.
func (m *Model) vimDeleteSelection() {
	content := m.editor.Value()
	lines := strings.Split(content, "\n")

	startLine := min(m.visualStart, m.visualEnd)
	endLine := max(m.visualStart, m.visualEnd)

	// Bounds check
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	// Store in yank buffer
	m.yankBuffer = strings.Join(lines[startLine:endLine+1], "\n")

	// Remove the lines
	newLines := append(lines[:startLine], lines[endLine+1:]...)
	if len(newLines) == 0 {
		newLines = []string{""}
	}
	m.editor.SetValue(strings.Join(newLines, "\n"))

	// Position cursor
	targetRow := startLine
	if targetRow >= len(newLines) {
		targetRow = len(newLines) - 1
	}

	// Reset cursor and navigate
	m.editor.SetCursor(0)
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
	for i := 0; i < targetRow; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})

	// Exit visual mode
	m.visualMode = false

	lineCount := endLine - startLine + 1
	if lineCount == 1 {
		m.setStatus("Deleted 1 line")
	} else {
		m.setStatus(fmt.Sprintf("Deleted %d lines", lineCount))
	}
}

// vimYankSelection yanks the selected lines in visual mode.
func (m *Model) vimYankSelection() {
	content := m.editor.Value()
	lines := strings.Split(content, "\n")

	startLine := min(m.visualStart, m.visualEnd)
	endLine := max(m.visualStart, m.visualEnd)

	// Bounds check
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	// Store in yank buffer
	m.yankBuffer = strings.Join(lines[startLine:endLine+1], "\n")

	// Exit visual mode
	m.visualMode = false

	lineCount := endLine - startLine + 1
	if lineCount == 1 {
		m.setStatus("Yanked 1 line")
	} else {
		m.setStatus(fmt.Sprintf("Yanked %d lines", lineCount))
	}
}

// vimInsertLineBelow inserts a new line below and enters insert mode.
func (m *Model) vimInsertLineBelow() {
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.syncDocFromEditor()
}

// vimInsertLineAbove inserts a new line above and enters insert mode.
func (m *Model) vimInsertLineAbove() {
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.syncDocFromEditor()
}

// vimWordForward moves cursor to next word.
func (m *Model) vimWordForward() {
	content := m.editor.Value()
	row := m.editor.Line()
	col := m.editor.LineInfo().ColumnOffset
	lines := strings.Split(content, "\n")

	if row >= len(lines) {
		return
	}

	line := lines[row]
	// Find next word boundary
	inWord := col < len(line) && !isWordChar(rune(line[col]))
	for col < len(line) {
		if inWord && isWordChar(rune(line[col])) {
			break
		}
		if !inWord && !isWordChar(rune(line[col])) {
			inWord = true
		}
		col++
	}

	// If at end of line, move to next line start
	if col >= len(line) && row < len(lines)-1 {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
		return
	}

	// Move cursor to target column
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
	for i := 0; i < col; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
}

// vimWordBackward moves cursor to previous word.
func (m *Model) vimWordBackward() {
	content := m.editor.Value()
	row := m.editor.Line()
	col := m.editor.LineInfo().ColumnOffset
	lines := strings.Split(content, "\n")

	if row >= len(lines) {
		return
	}

	line := lines[row]
	// Move back one char first
	if col > 0 {
		col--
	} else if row > 0 {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyUp})
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyEnd})
		return
	}

	// Skip non-word chars
	for col > 0 && col < len(line) && !isWordChar(rune(line[col])) {
		col--
	}
	// Skip word chars to find start
	for col > 0 && isWordChar(rune(line[col-1])) {
		col--
	}

	// Move cursor to target column
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
	for i := 0; i < col; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// vimGotoStart moves to document start.
func (m *Model) vimGotoStart() {
	m.editor.SetCursor(0)
	for m.editor.Line() > 0 {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
}

// vimGotoEnd moves to document end.
func (m *Model) vimGotoEnd() {
	content := m.editor.Value()
	lines := strings.Split(content, "\n")
	for m.editor.Line() < len(lines)-1 {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
}

// vimDeleteChar deletes char under cursor (x command).
func (m *Model) vimDeleteChar() {
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDelete})
}

// vimDeleteLine deletes current line (dd command).
func (m *Model) vimDeleteLine() {
	content := m.editor.Value()
	lines := strings.Split(content, "\n")
	row := m.editor.Line()

	if row >= len(lines) {
		return
	}

	// Store in yank buffer
	m.yankBuffer = lines[row]

	// Remove the line
	newLines := append(lines[:row], lines[row+1:]...)
	m.editor.SetValue(strings.Join(newLines, "\n"))

	// Position cursor: after SetValue(), cursor may be inconsistent.
	// First reset to document start, then navigate to target row.
	if row >= len(newLines) && row > 0 {
		row = len(newLines) - 1
	}

	// Reset cursor to beginning by going to position 0 and ensuring we're at line 0
	m.editor.SetCursor(0)
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})

	// Navigate down to target row
	for i := 0; i < row; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	// Go to beginning of line
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
}

// vimYankLine yanks current line (yy command).
func (m *Model) vimYankLine() {
	content := m.editor.Value()
	lines := strings.Split(content, "\n")
	row := m.editor.Line()

	if row < len(lines) {
		m.yankBuffer = lines[row]
		m.setStatus("Yanked line")
	}
}

// vimPaste pastes yanked line(s) below (p command).
func (m *Model) vimPaste() {
	if m.yankBuffer == "" {
		return
	}

	content := m.editor.Value()
	lines := strings.Split(content, "\n")
	row := m.editor.Line()

	// Split yank buffer in case it contains multiple lines
	yankLines := strings.Split(m.yankBuffer, "\n")

	// Insert after current line
	newLines := make([]string, 0, len(lines)+len(yankLines))
	newLines = append(newLines, lines[:row+1]...)
	newLines = append(newLines, yankLines...)
	if row+1 < len(lines) {
		newLines = append(newLines, lines[row+1:]...)
	}

	m.editor.SetValue(strings.Join(newLines, "\n"))

	// Move to the first pasted line
	for m.editor.Line() <= row {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyHome})
}
