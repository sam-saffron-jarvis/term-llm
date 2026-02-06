package plan

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/ui"
)

func newTestModel() *Model {
	s := spinner.New()
	s.Spinner = spinner.Dot

	ta := textarea.New()
	ta.SetWidth(76)
	ta.SetHeight(15)
	ta.Focus()

	return &Model{
		width:   80,
		height:  24,
		doc:     NewPlanDocument(),
		editor:  ta,
		spinner: s,
		styles:  ui.DefaultStyles(),
		tracker: ui.NewToolTracker(),
		config:  &config.Config{},
	}
}

func TestRenderActivityPanel_NoAgent(t *testing.T) {
	m := newTestModel()
	// No agent active, no stats => panel should be empty
	panel := m.renderActivityPanel()
	if panel != "" {
		t.Errorf("expected empty panel when no agent active, got: %q", panel)
	}
}

func TestRenderActivityPanel_AgentActive(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Thinking"
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	m.activityExpanded = true

	panel := m.renderActivityPanel()
	if panel == "" {
		t.Fatal("expected non-empty panel when agent active")
	}
	if !strings.Contains(panel, "Agent") {
		t.Error("panel should contain 'Agent' label")
	}
	// Status (phase/stats) is only shown in the bottom status line, not in the panel
}

func TestRenderActivityPanel_WithCompletedTools(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Thinking"
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	m.activityExpanded = true

	// Add a completed tool
	m.tracker.HandleToolStart("call-1", "grep", "(pattern)")
	m.tracker.HandleToolEnd("call-1", true)

	panel := m.renderActivityPanel()
	if !strings.Contains(panel, "grep") {
		t.Error("panel should show completed tool 'grep'")
	}
}

func TestRenderActivityPanel_WithPendingTool(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Using grep..."
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	m.activityExpanded = true

	// Add a pending tool
	m.tracker.HandleToolStart("call-1", "grep", "(pattern)")

	panel := m.renderActivityPanel()
	if !strings.Contains(panel, "grep") {
		t.Error("panel should show pending tool 'grep'")
	}
}

func TestRenderActivityPanel_Collapsed(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Thinking"
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	m.activityExpanded = false

	// Collapsed panel should be empty; status is shown only in the bottom status line
	panel := m.renderActivityPanel()
	if panel != "" {
		t.Errorf("expected empty panel when collapsed, got: %q", panel)
	}
}

func TestRenderActivityPanel_WithReasoningText(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Analyzing"
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	m.activityExpanded = true
	m.agentText.WriteString("Looking at the codebase structure\n")

	panel := m.renderActivityPanel()
	if !strings.Contains(panel, "Looking at the codebase structure") {
		t.Error("panel should show last line of reasoning text")
	}
}

func TestActivityPanelHeight(t *testing.T) {
	m := newTestModel()

	// No agent = 0 height
	if h := m.activityPanelHeight(); h != 0 {
		t.Errorf("expected 0 height when no agent, got %d", h)
	}

	// Agent active + expanded = at least separators (2)
	m.agentActive = true
	m.stats = ui.NewSessionStats()
	m.activityExpanded = true
	h := m.activityPanelHeight()
	if h < 2 {
		t.Errorf("expected at least 2 lines for expanded panel, got %d", h)
	}

	// Collapsed = 0 (status only shown in bottom status line)
	m.activityExpanded = false
	if h := m.activityPanelHeight(); h != 0 {
		t.Errorf("expected 0 lines for collapsed panel, got %d", h)
	}
}

func TestSyncEditorFromDoc_PreservesCursor(t *testing.T) {
	m := newTestModel()

	// Set initial content and position cursor on a middle line
	initial := "line 0\nline 1\nline 2\nline 3\nline 4"
	m.editor.SetValue(initial)
	m.editor.SetCursor(0)
	// Move to line 3
	for i := 0; i < 3; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	savedLine := m.editor.Line()
	if savedLine == 0 {
		t.Fatal("setup: cursor should not be on line 0")
	}

	// Set doc content (slightly different, same line count)
	m.doc.SetText("line 0\nline 1\nline 2 modified\nline 3\nline 4", "agent")

	// Sync - cursor should not reset to 0
	m.syncEditorFromDoc()

	restoredLine := m.editor.Line()
	if restoredLine == 0 {
		t.Error("cursor was reset to line 0 after sync - position not preserved")
	}
	if restoredLine != savedLine {
		// Allow some drift due to content changes, but it should be close
		diff := restoredLine - savedLine
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			t.Errorf("cursor drifted too far: was %d, now %d", savedLine, restoredLine)
		}
	}
}

func TestStreamEventUsage_UpdatesStats(t *testing.T) {
	m := newTestModel()
	m.stats = ui.NewSessionStats()

	ev := ui.StreamEvent{
		Type:         ui.StreamEventUsage,
		InputTokens:  100,
		OutputTokens: 50,
		CachedTokens: 20,
	}
	m.handleStreamEvent(ev)

	if m.stats.InputTokens != 100 {
		t.Errorf("expected InputTokens=100, got %d", m.stats.InputTokens)
	}
	if m.stats.OutputTokens != 50 {
		t.Errorf("expected OutputTokens=50, got %d", m.stats.OutputTokens)
	}
	if m.stats.CachedInputTokens != 20 {
		t.Errorf("expected CachedInputTokens=20, got %d", m.stats.CachedInputTokens)
	}
	if m.stats.LLMCallCount != 1 {
		t.Errorf("expected LLMCallCount=1, got %d", m.stats.LLMCallCount)
	}
	if m.currentTurn != 1 {
		t.Errorf("expected currentTurn=1, got %d", m.currentTurn)
	}
}

func TestStreamEventToolStart_TracksStats(t *testing.T) {
	m := newTestModel()
	m.stats = ui.NewSessionStats()

	ev := ui.StreamEvent{
		Type:       ui.StreamEventToolStart,
		ToolCallID: "call-1",
		ToolName:   "grep",
		ToolInfo:   "(pattern)",
	}
	m.handleStreamEvent(ev)

	if m.stats.ToolCallCount != 1 {
		t.Errorf("expected ToolCallCount=1, got %d", m.stats.ToolCallCount)
	}
}

func TestStreamEventText_CapturesReasoningText(t *testing.T) {
	m := newTestModel()
	m.agentPhase = "Thinking"

	ev := ui.StreamEvent{
		Type: ui.StreamEventText,
		Text: "Looking at the code...",
	}
	m.handleStreamEvent(ev)

	if m.agentText.String() != "Looking at the code..." {
		t.Errorf("expected reasoning text captured, got %q", m.agentText.String())
	}
	if m.agentPhase != "Analyzing" {
		t.Errorf("expected phase 'Analyzing', got %q", m.agentPhase)
	}
}

func TestStreamEventText_DoesNotOverrideInsertPhase(t *testing.T) {
	m := newTestModel()
	m.agentPhase = "Inserting"

	ev := ui.StreamEvent{
		Type: ui.StreamEventText,
		Text: "some text",
	}
	m.handleStreamEvent(ev)

	if m.agentPhase != "Inserting" {
		t.Errorf("expected phase to stay 'Inserting', got %q", m.agentPhase)
	}
}

func TestStatusLine_AgentActive_ShowsStats(t *testing.T) {
	m := newTestModel()
	m.agentActive = true
	m.agentPhase = "Thinking"
	m.stats = ui.NewSessionStats()
	m.stats.AddUsage(1000, 500, 0)
	m.stats.ToolStart()
	m.stats.ToolEnd()
	m.stats.ToolStart()
	m.stats.ToolEnd()
	m.streamStartTime = time.Now().Add(-5 * time.Second)
	m.currentTurn = 2

	line := m.renderStatusLine()

	if !strings.Contains(line, "Thinking") {
		t.Error("status line should contain phase")
	}
	if !strings.Contains(line, "turn 2") {
		t.Error("status line should contain turn count")
	}
	if !strings.Contains(line, "2 tools") {
		t.Error("status line should contain tool count")
	}
	if !strings.Contains(line, "tok") {
		t.Error("status line should contain token info")
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"hello\n", "hello"},
		{"hello\nworld\n", "world"},
		{"hello\n\n\n", "hello"},
		{"  \n  \nhello\n  \n", "hello"},
	}

	for _, tt := range tests {
		got := lastNonEmptyLine(tt.input)
		if got != tt.want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGetContent_ReturnsEditorContent(t *testing.T) {
	m := newTestModel()
	m.editor.SetValue("# My Plan\n\n- Step 1\n- Step 2")
	content := m.GetContent()
	if !strings.Contains(content, "Step 1") {
		t.Errorf("GetContent should return editor content, got %q", content)
	}
}

func TestHandedOff_DefaultFalse(t *testing.T) {
	m := newTestModel()
	if m.HandedOff() {
		t.Error("HandedOff should be false by default")
	}
}

func TestHandoff_SetsHandedOff(t *testing.T) {
	m := newTestModel()
	m.editor.SetValue("# Plan content")
	m.syncDocFromEditor()

	// Trigger handoff via Ctrl+G — this now shows the agent picker
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	rm := result.(*Model)

	// Agent picker should be visible
	if !rm.agentPickerVisible {
		t.Fatal("agent picker should be visible after Ctrl+G")
	}

	// Select and confirm
	result, cmd := rm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm = result.(*Model)

	if !rm.HandedOff() {
		t.Error("HandedOff should be true after confirming agent picker")
	}
	if rm.HandoffAgent() != "" {
		t.Error("first item (no agent) should yield empty agent name")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestHandoff_BlockedDuringAgentRun(t *testing.T) {
	m := newTestModel()
	m.editor.SetValue("# Plan content")
	m.syncDocFromEditor()
	m.agentActive = true

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	rm := result.(*Model)

	if rm.HandedOff() {
		t.Error("handoff should be blocked while agent is active")
	}
	if rm.agentPickerVisible {
		t.Error("agent picker should not show while agent is active")
	}
}

func TestHandoff_BlockedOnEmptyDoc(t *testing.T) {
	m := newTestModel()
	// Editor is empty by default

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	rm := result.(*Model)

	if rm.HandedOff() {
		t.Error("handoff should be blocked on empty document")
	}
	if rm.agentPickerVisible {
		t.Error("agent picker should not show for empty document")
	}
}

func TestHelpOverlay_CtrlHToggles(t *testing.T) {
	m := newTestModel()

	// Initially not visible
	if m.helpVisible {
		t.Fatal("help should not be visible initially")
	}

	// Press Ctrl+H to open
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	rm := result.(*Model)
	if !rm.helpVisible {
		t.Error("Ctrl+H should toggle help visible")
	}

	// Press any key to close
	result, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rm = result.(*Model)
	if rm.helpVisible {
		t.Error("any key should close help overlay")
	}
}

func TestStatusBar_CompactShortcuts(t *testing.T) {
	m := newTestModel()

	// Normal mode
	line := m.renderStatusLine()
	if !strings.Contains(line, "^H: help") {
		t.Error("status line should contain ^H: help")
	}
	if strings.Contains(line, "Ctrl+A: activity") {
		t.Error("status line should not contain verbose shortcuts")
	}

	// Agent active
	m.agentActive = true
	m.agentPhase = "Thinking"
	m.stats = ui.NewSessionStats()
	m.streamStartTime = time.Now()
	line = m.renderStatusLine()
	if !strings.Contains(line, "^H: help") {
		t.Error("agent-active status line should contain ^H: help")
	}
	if !strings.Contains(line, "Esc: cancel") {
		t.Error("agent-active status line should contain Esc: cancel")
	}
}

func TestGoCommand_TriggersHandoff(t *testing.T) {
	m := newTestModel()
	m.editor.SetValue("# Plan content")
	m.syncDocFromEditor()
	m.vimMode = true

	// Enter command mode with ":"
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	rm := result.(*Model)
	if !rm.commandMode {
		t.Fatal("expected command mode after ':'")
	}

	// Type "go"
	result, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	rm = result.(*Model)
	result, _ = rm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	rm = result.(*Model)

	// Press enter to execute
	result, _ = rm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm = result.(*Model)

	// Agent picker should be visible
	if !rm.agentPickerVisible {
		t.Error(":go command should show agent picker")
	}
}

func TestRenderHelpOverlay_ContainsShortcuts(t *testing.T) {
	m := newTestModel()
	overlay := m.renderHelpOverlay()

	shortcuts := []string{"Ctrl+P", "Ctrl+S", "Ctrl+G", "Ctrl+A", "Ctrl+H", "Ctrl+C", "Ctrl+K"}
	for _, s := range shortcuts {
		if !strings.Contains(overlay, s) {
			t.Errorf("help overlay should contain %q", s)
		}
	}
}

func TestAgentPicker_EscCancels(t *testing.T) {
	m := newTestModel()
	m.editor.SetValue("# Plan content")
	m.syncDocFromEditor()

	// Trigger handoff to show picker
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	rm := result.(*Model)
	if !rm.agentPickerVisible {
		t.Fatal("agent picker should be visible")
	}

	// Press Esc to cancel
	result, _ = rm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm = result.(*Model)
	if rm.agentPickerVisible {
		t.Error("Esc should close agent picker")
	}
	if rm.HandedOff() {
		t.Error("Esc should not trigger handoff")
	}
}
