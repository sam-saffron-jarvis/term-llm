package chat

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/samsaffron/term-llm/internal/session"
)

func (m *Model) handlePromptHistoryKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	up := key.Matches(msg, m.keyMap.HistoryUp)
	down := key.Matches(msg, m.keyMap.HistoryDown)
	if !up && !down {
		return false, nil
	}

	if up {
		if m.tryMoveTextareaUp() {
			m.resetPromptHistoryIfEdited()
			m.updateTextareaHeight()
			return true, nil
		}
		return m.recallPreviousPrompt()
	}

	if m.tryMoveTextareaDown() {
		m.resetPromptHistoryIfEdited()
		m.updateTextareaHeight()
		return true, nil
	}
	return m.recallNextPrompt()
}

func (m *Model) tryMoveTextareaUp() bool {
	line := m.textarea.Line()
	col := m.textarea.Column()
	y := m.textarea.ScrollYOffset()
	m.textarea.CursorUp()
	return m.textarea.Line() != line || m.textarea.Column() != col || m.textarea.ScrollYOffset() != y
}

func (m *Model) tryMoveTextareaDown() bool {
	line := m.textarea.Line()
	col := m.textarea.Column()
	y := m.textarea.ScrollYOffset()
	m.textarea.CursorDown()
	return m.textarea.Line() != line || m.textarea.Column() != col || m.textarea.ScrollYOffset() != y
}

func (m *Model) recallPreviousPrompt() (bool, tea.Cmd) {
	history, ok := m.store.(session.PromptHistoryStore)
	if !ok || history == nil {
		return false, nil
	}

	current := m.textarea.Value()
	if !m.promptHistory.active {
		m.promptHistory = promptHistoryState{
			active:      true,
			draftText:   current,
			draftFiles:  append([]FileAttachment(nil), m.files...),
			draftImages: append([]ImageAttachment(nil), m.images...),
			draftPastes: clonePasteChunks(m.pasteChunks),
		}
	} else if current != m.promptHistory.recalledText {
		m.resetPromptHistory()
		return false, nil
	}

	entry, err := history.PreviousUserPrompt(context.Background(), m.agentName, m.promptHistory.cursorID)
	if err != nil {
		m.showPromptHistoryError(err)
		return true, nil
	}
	if entry == nil {
		return true, nil
	}

	m.applyPromptHistoryEntry(entry)
	return true, nil
}

func (m *Model) recallNextPrompt() (bool, tea.Cmd) {
	if !m.promptHistory.active {
		return false, nil
	}
	if m.textarea.Value() != m.promptHistory.recalledText {
		m.resetPromptHistory()
		return false, nil
	}

	history, ok := m.store.(session.PromptHistoryStore)
	if !ok || history == nil {
		m.restorePromptHistoryDraft()
		return true, nil
	}

	entry, err := history.NextUserPrompt(context.Background(), m.agentName, m.promptHistory.cursorID)
	if err != nil {
		m.showPromptHistoryError(err)
		return true, nil
	}
	if entry == nil {
		m.restorePromptHistoryDraft()
		return true, nil
	}

	m.applyPromptHistoryEntry(entry)
	m.textarea.MoveToEnd()
	return true, nil
}

func (m *Model) applyPromptHistoryEntry(entry *session.PromptHistoryEntry) {
	m.promptHistory.cursorID = entry.ID
	m.promptHistory.recalledText = entry.Text
	m.textarea.SetValue(entry.Text)
	m.textarea.MoveToBegin()
	m.updateTextareaHeight()

	// Prompt history rows are text-only. Keep the draft attachments out of the
	// recalled prompt so Enter does not accidentally send stale files/images.
	m.files = nil
	m.images = nil
	m.selectedImage = -1
	m.pasteChunks = nil
}

func (m *Model) restorePromptHistoryDraft() {
	draftText := m.promptHistory.draftText
	draftFiles := append([]FileAttachment(nil), m.promptHistory.draftFiles...)
	draftImages := append([]ImageAttachment(nil), m.promptHistory.draftImages...)
	draftPastes := clonePasteChunks(m.promptHistory.draftPastes)
	m.resetPromptHistory()
	m.textarea.SetValue(draftText)
	m.textarea.MoveToEnd()
	m.updateTextareaHeight()
	m.files = draftFiles
	m.images = draftImages
	m.selectedImage = -1
	m.pasteChunks = draftPastes
}

func (m *Model) resetPromptHistoryIfEdited() {
	if m.promptHistory.active && m.textarea.Value() != m.promptHistory.recalledText {
		m.resetPromptHistory()
	}
}

func (m *Model) resetPromptHistory() {
	m.promptHistory = promptHistoryState{}
}

func (m *Model) showPromptHistoryError(err error) {
	m.phase = fmt.Sprintf("Prompt history error: %v", err)
}

func clonePasteChunks(in map[int]string) map[int]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
