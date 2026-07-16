package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
)

func TestSideCommandsForkReturnAndCloseExplicitly(t *testing.T) {
	store, err := session.NewSQLiteStore(session.Config{Path: t.TempDir() + "/sessions.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent := &session.Session{ID: "parent", Provider: "mock", Model: "model"}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(context.Background(), parent.ID, session.NewMessage(parent.ID, llm.UserText("context"), -1)); err != nil {
		t.Fatal(err)
	}

	m := newCmdTestModel(store)
	m.sess = parent
	result, _ := m.ExecuteCommand("/side investigate this")
	forked := result.(*Model)
	if !forked.quitting || forked.pendingResumeSessionID == "" || forked.pendingHandoverAutoSend != "investigate this" {
		t.Fatalf("fork state: resume=%q auto=%q quitting=%v", forked.pendingResumeSessionID, forked.pendingHandoverAutoSend, forked.quitting)
	}
	side, err := store.Get(context.Background(), forked.pendingResumeSessionID)
	if err != nil || side == nil || side.Kind != session.KindSide {
		t.Fatalf("side=%+v err=%v", side, err)
	}

	m = newCmdTestModel(store)
	m.sess = side
	result, _ = m.ExecuteCommand("/main")
	returned := result.(*Model)
	if returned.pendingResumeSessionID != parent.ID {
		t.Fatalf("main resume=%q", returned.pendingResumeSessionID)
	}
	stillOpen, _ := store.Get(context.Background(), side.ID)
	if stillOpen.SideState != session.SideOpen {
		t.Fatal("/main implicitly closed side")
	}

	m = newCmdTestModel(store)
	m.sess = side
	_, _ = m.ExecuteCommand("/side close")
	closed, _ := store.Get(context.Background(), side.ID)
	if closed.SideState != session.SideClosed {
		t.Fatalf("side state=%s", closed.SideState)
	}
}

func TestSideBuildMessagesIncludesHiddenInheritedContextAndPolicy(t *testing.T) {
	m := newTestChatModel(false)
	m.sess = &session.Session{ID: "side", Kind: session.KindSide, SideState: session.SideOpen}
	m.inheritedBase = []llm.Message{llm.UserText("hidden parent context")}
	m.messages = []session.Message{*session.NewMessage("side", llm.UserText("local side prompt"), 0)}
	messages := m.buildMessagesForStream()
	var joined string
	for _, msg := range messages {
		joined += llm.MessageText(msg) + "\n"
	}
	for _, want := range []string{"reference-only", "hidden parent context", "local side prompt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("messages missing %q: %s", want, joined)
		}
	}
	if len(m.messages) != 1 {
		t.Fatal("inherited context polluted display transcript")
	}
}
