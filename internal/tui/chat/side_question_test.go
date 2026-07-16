package chat

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/sidequestion"
)

func TestSideCommandOpensOverlayWithoutChangingConversation(t *testing.T) {
	m := newTestChatModel(true)
	m.sess = &session.Session{ID: "main-session"}
	m.messages = []session.Message{{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "main fact"}}}}
	m.scrollOffset = 3
	m.textarea.SetValue("preserved draft")
	provider := llm.NewMockProvider("mock").AddTextResponse("side answer")
	m.SetSideQuestionProviderFactory(func(_, _ string) (llm.Provider, error) { return provider, nil })

	updated, cmd := m.ExecuteCommand("/side what does that mean?")
	m = updated.(*Model)
	if !m.sideQuestion.Visible || !m.sideQuestion.Running || m.sess.ID != "main-session" || cmd == nil {
		t.Fatalf("side state = visible %v running %v session %q cmd %v", m.sideQuestion.Visible, m.sideQuestion.Running, m.sess.ID, cmd != nil)
	}
	if m.scrollOffset != 3 || m.textarea.Value() != "preserved draft" {
		t.Fatalf("overlay changed main UI state: scroll=%d draft=%q", m.scrollOffset, m.textarea.Value())
	}
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		updated, cmd = m.Update(msg)
		m = updated.(*Model)
	}
	if m.sideQuestion.Running || len(m.sideQuestion.History) != 1 || m.sideQuestion.History[0].Response != "side answer" {
		t.Fatalf("completed side state = %#v", m.sideQuestion)
	}
	if len(m.messages) != 1 {
		t.Fatalf("side content entered transcript: %#v", m.messages)
	}
}

func TestSideSnapshotExcludesActiveMainTurn(t *testing.T) {
	m := newTestChatModel(true)
	m.messages = []session.Message{
		{Role: llm.RoleSystem, Parts: []llm.Part{{Type: llm.PartText, Text: "system"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "completed question"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartText, Text: "completed answer"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "active question"}}},
	}
	m.streaming = true
	got := m.sideSnapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot len = %d, want 3: %#v", len(got), got)
	}
}

func TestSideCancellationDoesNotCancelMain(t *testing.T) {
	m := newTestChatModel(true)
	mainCtx, mainCancel := context.WithCancel(context.Background())
	defer mainCancel()
	sideCtx, sideCancel := context.WithCancel(context.Background())
	m.streamCancelFunc = mainCancel
	m.sideQuestion = SideQuestionState{Visible: true, Running: true, Cancel: sideCancel, Generation: 1}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(*Model)
	if sideCtx.Err() == nil {
		t.Fatal("side context was not cancelled")
	}
	if mainCtx.Err() != nil {
		t.Fatal("side cancellation cancelled main")
	}
}

func TestLateSideGenerationIgnoredAndClearConfirmed(t *testing.T) {
	m := newTestChatModel(true)
	m.sideQuestion = SideQuestionState{Visible: true, Generation: 2, History: []sidequestion.Entry{{Question: "q", Response: "a"}}}
	_, _ = m.Update(sideQuestionEventMsg{generation: 1, event: llm.Event{Type: llm.EventTextDelta, Text: "late"}})
	if m.sideQuestion.Response.Len() != 0 {
		t.Fatal("late side event was applied")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
	if !m.sideQuestion.ConfirmClear || len(m.sideQuestion.History) != 1 {
		t.Fatal("first x should only confirm")
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
	if len(m.sideQuestion.History) != 0 || m.sideQuestion.Visible {
		t.Fatal("second x did not clear history")
	}
}

func TestSideAndOnlySideIsStreamingLocalCommand(t *testing.T) {
	if !isStreamingLocalSlashCommand("/side question") {
		t.Fatal("/side should be available while main streams")
	}
	if isStreamingLocalSlashCommand("/main") || isSlashCommandLike("/main") {
		t.Fatal("/main should not exist")
	}
}
