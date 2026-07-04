package chat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/tools"
)

func TestSetApprovalModePersistsToSession(t *testing.T) {
	store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	sess := &session.Session{ID: session.NewID(), Provider: "mock", Model: "mock", Mode: session.ModeChat}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := newTestChatModel(false)
	m.store = store
	m.sess = sess
	m.SetApprovalManager(tools.NewApprovalManager(tools.NewToolPermissions()))

	m.setApprovalMode(tools.ModeAuto)
	got, err := store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ApprovalMode != session.ApprovalModeAuto {
		t.Fatalf("ApprovalMode = %q, want auto", got.ApprovalMode)
	}
}
