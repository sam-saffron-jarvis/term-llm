package session

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteStorePersistsApprovalMode(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	sess := &Session{ID: NewID(), Provider: "mock", Model: "mock", Mode: ModeChat, ApprovalMode: ApprovalModeAuto}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ApprovalMode != ApprovalModeAuto {
		t.Fatalf("ApprovalMode after create = %q, want %q", got.ApprovalMode, ApprovalModeAuto)
	}

	got.ApprovalMode = ApprovalModePrompt
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.ApprovalMode != ApprovalModePrompt {
		t.Fatalf("ApprovalMode after update = %q, want %q", got.ApprovalMode, ApprovalModePrompt)
	}
}
