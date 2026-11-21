package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/term-llm/term-llm/internal/ui"
)

func TestSlashEnterActivatesVisibleEleventhRow(t *testing.T) {
	m := newTestChatModel(false)
	m.setTextareaValue("/")
	items := make([]Command, 10)
	for i := range items {
		items[i].Name = "model"
	}
	m.completions.SetItems(append(items, Command{Name: "help"}))
	for range 10 {
		_, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if view := ui.StripANSI(m.completions.View()); !strings.Contains(view, "❯ /help") {
		t.Fatalf("eleventh command is not highlighted:\n%s", view)
	}
	_, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.dialog.Type() != DialogContent || m.completions.IsVisible() {
		t.Fatal("Enter did not open /help")
	}
}

func TestModelPickerCtrlNavigation(t *testing.T) {
	m := newTestChatModel(false)
	m.dialog.ShowModelPicker("mock:first", []ProviderInfo{{Name: "mock", Models: []string{"first", "second"}}}, nil)
	for _, tt := range []struct {
		key  rune
		want string
	}{{'n', "mock:second"}, {'p', "mock:first"}} {
		_, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tt.key, Mod: tea.ModCtrl})
		if selected := m.dialog.Selected(); selected == nil || selected.ID != tt.want {
			t.Fatalf("Ctrl+%c selected %#v, want %s", tt.key, selected, tt.want)
		}
	}
}
