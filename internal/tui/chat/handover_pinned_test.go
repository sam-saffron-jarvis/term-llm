package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/agents"
	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/session"
)

// TestCmdHandoverFileModePrefersPinnedPath ensures /handover reads the exact
// file agents are told about via {{handover_path}}, even when another .md in
// the handover directory has a newer modification time.
func TestCmdHandoverFileModePrefersPinnedPath(t *testing.T) {
	tmp := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg-data"))
	session.ResetHandoverPathCache()
	t.Cleanup(session.ResetHandoverPathCache)

	pinned, err := session.GetHandoverPath(".", time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatalf("GetHandoverPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(pinned), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(pinned, []byte("the pinned plan"), 0o644); err != nil {
		t.Fatalf("WriteFile pinned: %v", err)
	}

	// A stray document with a newer mtime must not shadow the pinned plan.
	decoy := filepath.Join(filepath.Dir(pinned), "2026-01-01-stray-notes.md")
	if err := os.WriteFile(decoy, []byte("stray notes"), 0o644); err != nil {
		t.Fatalf("WriteFile decoy: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(decoy, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	m := newTestChatModel(false)
	m.store = &mockStore{}
	m.config = &config.Config{}
	m.sess = &session.Session{ID: "pinned-handover", CreatedAt: time.Now().Add(-time.Minute)}
	m.agentName = "planner"
	m.currentAgent = &agents.Agent{Name: "planner", EnableHandover: true, HandoverMode: "file"}
	m.agentResolver = func(name string, cfg *config.Config) (*agents.Agent, error) {
		return &agents.Agent{Name: name, SystemPrompt: "You are target."}, nil
	}

	_, cmd := m.cmdHandover([]string{"@developer"})
	if cmd == nil {
		t.Fatal("expected handover command")
	}
	msg := cmd()
	done, ok := msg.(handoverDoneMsg)
	if !ok {
		t.Fatalf("handover command returned %T, want handoverDoneMsg", msg)
	}
	if done.result == nil {
		t.Fatal("expected handover result")
	}
	if done.result.Document != "the pinned plan" {
		t.Fatalf("handover used wrong document: %q", done.result.Document)
	}
}
