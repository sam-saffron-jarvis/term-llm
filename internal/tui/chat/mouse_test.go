package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMouseClickMovesCursorSingleLine(t *testing.T) {
	m := newTestChatModel(false)
	m.setTextareaValue("hello world")
	_ = m.View()

	clickX := m.textareaLeftX + m.textareaPromptWidth + 5
	clickY := m.textareaTopY

	_, _ = m.Update(tea.MouseClickMsg{
		X:      clickX,
		Y:      clickY,
		Button: tea.MouseLeft,
	})

	if got := m.textarea.Line(); got != 0 {
		t.Fatalf("line = %d, want 0", got)
	}
	if got := m.textarea.LineInfo().CharOffset; got != 5 {
		t.Fatalf("char offset = %d, want 5", got)
	}
}

func TestMouseClickMovesCursorWrappedLine(t *testing.T) {
	m := newTestChatModel(false)
	m.width = 12
	m.textarea.SetWidth(12)
	m.setTextareaValue("abcdefghijk")
	_ = m.View()

	if m.textarea.Height() < 2 {
		t.Fatalf("expected wrapped textarea height >= 2, got %d", m.textarea.Height())
	}

	clickX := m.textareaLeftX + m.textareaPromptWidth + 2
	clickY := m.textareaTopY + 1

	_, _ = m.Update(tea.MouseClickMsg{
		X:      clickX,
		Y:      clickY,
		Button: tea.MouseLeft,
	})

	if got := m.textarea.Line(); got != 0 {
		t.Fatalf("line = %d, want 0", got)
	}
	if got := m.textarea.LineInfo().RowOffset; got != 1 {
		t.Fatalf("row offset = %d, want 1 for wrapped-line click", got)
	}
	if got := m.textarea.LineInfo().CharOffset; got == 0 {
		t.Fatalf("char offset = %d, want > 0 on wrapped row", got)
	}
}

func TestMouseShiftClickDoesNotMoveCursor(t *testing.T) {
	m := newTestChatModel(false)
	m.setTextareaValue("hello world")
	_ = m.View()
	m.textarea.CursorStart()

	clickX := m.textareaLeftX + m.textareaPromptWidth + 5
	clickY := m.textareaTopY

	_, _ = m.Update(tea.MouseClickMsg{
		X:      clickX,
		Y:      clickY,
		Mod:    tea.ModShift,
		Button: tea.MouseLeft,
	})

	if got := m.textarea.LineInfo().CharOffset; got != 0 {
		t.Fatalf("char offset = %d, want 0", got)
	}
}

func TestMouseClickMovesCursorInAltScreen(t *testing.T) {
	m := newTestChatModel(true)
	m.setTextareaValue("hello world")
	_ = m.View()

	clickX := m.textareaLeftX + m.textareaPromptWidth + 4
	clickY := m.textareaTopY

	_, _ = m.Update(tea.MouseClickMsg{
		X:      clickX,
		Y:      clickY,
		Button: tea.MouseLeft,
	})

	if got := m.textarea.LineInfo().CharOffset; got != 4 {
		t.Fatalf("char offset = %d, want 4", got)
	}
}

func TestMouseWheelScrollStillWorksInAltScreen(t *testing.T) {
	m := newTestChatModel(true)
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line")
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoTop()

	_, _ = m.Update(tea.MouseWheelMsg{
		X:      0,
		Y:      0,
		Button: tea.MouseWheelDown,
	})

	if m.viewport.YOffset() == 0 {
		t.Fatal("expected viewport to scroll on mouse wheel down")
	}
}

func TestMouseWheelScrollsContentDialogInsteadOfViewport(t *testing.T) {
	m := newTestChatModel(true)
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line")
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoTop()
	m.dialog.ShowContent("Help", strings.Join(lines, "\n"))

	_, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if m.dialog.contentScroll == 0 {
		t.Fatal("expected modal content to scroll on mouse wheel down")
	}
	if got := m.viewport.YOffset(); got != 0 {
		t.Fatalf("underlying viewport scrolled while modal was open: %d", got)
	}

	m.dialog.Close()
	_, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.viewport.YOffset() == 0 {
		t.Fatal("expected viewport to scroll after modal closes")
	}
}

func TestHorizontalMouseWheelDoesNotShiftAltScreenViewport(t *testing.T) {
	m := newTestChatModel(true)
	m.viewport.SetContent(strings.Repeat("x", 200))
	m.viewport.SetXOffset(12)
	if m.viewport.XOffset() == 0 {
		t.Fatal("precondition: expected horizontal offset to be set")
	}

	_, _ = m.Update(tea.MouseWheelMsg{
		X:      0,
		Y:      0,
		Button: tea.MouseWheelRight,
	})
	if got := m.viewport.XOffset(); got != 0 {
		t.Fatalf("horizontal wheel left x-offset = %d, want 0", got)
	}

	m.viewport.SetXOffset(12)
	_, _ = m.Update(tea.MouseWheelMsg{
		X:      0,
		Y:      0,
		Button: tea.MouseWheelUp,
		Mod:    tea.ModShift,
	})
	if got := m.viewport.XOffset(); got != 0 {
		t.Fatalf("shift-wheel x-offset = %d, want 0", got)
	}
}

func TestChatDisableMouseEnvDisablesMouseReporting(t *testing.T) {
	t.Setenv(chatDisableMouseEnv, "1")
	m := newTestChatModel(true)

	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Fatalf("MouseMode = %v, want MouseModeNone", got)
	}
}

func TestMiddleClickPasteWorksWhileStreaming(t *testing.T) {
	m := newTestChatModel(true)
	m.streaming = true

	orig := readPrimarySelection
	readPrimarySelection = func() (string, error) { return "interject from primary", nil }
	t.Cleanup(func() { readPrimarySelection = orig })

	_, _ = m.Update(tea.MouseClickMsg{
		X:      0,
		Y:      0,
		Button: tea.MouseMiddle,
	})

	if got := m.textarea.Value(); got != "interject from primary" {
		t.Fatalf("textarea value = %q, want pasted primary selection", got)
	}
}
