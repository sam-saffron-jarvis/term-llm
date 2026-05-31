package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/session"
)

// --- pure / guard-path tests (no git, no cwd dependence) ------------------

func TestWorktreeBaseDirPrefersBinding(t *testing.T) {
	m := newCmdTestModel(&mockStore{})
	m.sess = &session.Session{ID: "s1", WorktreeDir: "/tmp/wt-xyz"}
	if got := m.worktreeBaseDir(); got != "/tmp/wt-xyz" {
		t.Errorf("worktreeBaseDir() = %q, want bound dir", got)
	}
	if got := m.boundWorktreeDir(); got != "/tmp/wt-xyz" {
		t.Errorf("boundWorktreeDir() = %q, want bound dir", got)
	}

	m.sess = &session.Session{ID: "s1"}
	if got := m.boundWorktreeDir(); got != "" {
		t.Errorf("boundWorktreeDir() = %q, want empty on root", got)
	}

	m.sess = nil
	if got := m.boundWorktreeDir(); got != "" {
		t.Errorf("boundWorktreeDir() with nil session = %q, want empty", got)
	}
}

func TestWorktreeCommandsGuardWhenUnbound(t *testing.T) {
	cases := []struct {
		name string
		call func(m *Model) (interface{}, error)
		want string
	}{
		{"pwd", func(m *Model) (interface{}, error) { r, _ := m.cmdWorktreePwd(); return r, nil }, "root checkout"},
		{"diff", func(m *Model) (interface{}, error) { r, _ := m.cmdWorktreeDiff(); return r, nil }, "Not bound"},
		{"promote", func(m *Model) (interface{}, error) { r, _ := m.cmdWorktreePromote([]string{"b"}); return r, nil }, "Not bound"},
		{"rm", func(m *Model) (interface{}, error) { r, _ := m.cmdWorktreeRemove(nil); return r, nil }, "Not bound"},
		{"shell", func(m *Model) (interface{}, error) { r, _ := m.cmdWorktreeShell(nil); return r, nil }, "Not bound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCmdTestModel(&mockStore{})
			m.sess = &session.Session{ID: "s1"} // no WorktreeDir -> root checkout
			if _, err := tc.call(m); err != nil {
				t.Fatalf("call: %v", err)
			}
			if !strings.Contains(m.footerMessage, tc.want) {
				t.Errorf("footer = %q, want contains %q", m.footerMessage, tc.want)
			}
		})
	}
}

func TestCachedWorktreeSegmentEmptyOnRoot(t *testing.T) {
	m := newCmdTestModel(&mockStore{})
	m.sess = &session.Session{ID: "s1"}
	if seg := m.cachedWorktreeSegment(); seg != "" {
		t.Errorf("segment on root = %q, want empty", seg)
	}
	// A stale cache must be cleared once unbound.
	m.worktreeSegCache = "⌥ stale"
	if seg := m.cachedWorktreeSegment(); seg != "" {
		t.Errorf("segment after unbind = %q, want empty", seg)
	}
}

func TestPathsEqual(t *testing.T) {
	if !pathsEqual("/tmp/a", "/tmp/a") {
		t.Error("identical paths should be equal")
	}
	if pathsEqual("/tmp/a", "/tmp/b") {
		t.Error("different paths should not be equal")
	}
	if pathsEqual("", "/tmp/a") {
		t.Error("empty vs non-empty should not be equal")
	}
	if !pathsEqual("", "") {
		t.Error("empty vs empty should be equal")
	}
}

func TestCurrentTag(t *testing.T) {
	if currentTag(true) == "" {
		t.Error("currentTag(true) should be non-empty")
	}
	if currentTag(false) != "" {
		t.Error("currentTag(false) should be empty")
	}
}

// --- integration test (real git) -----------------------------------------

func initChatRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return root
}

// TestWorktreeNewBindDiffRemoveFlow drives the full TUI command surface against
// a real repo: create+bind, footer segment, pwd, diff, then force-remove back to
// root. It chdir's the process (the TUI's single-session binding mechanism), so
// it restores cwd and is not parallel-safe.
func TestWorktreeNewBindDiffRemoveFlow(t *testing.T) {
	repo := initChatRepo(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	store := &mockStore{sessions: map[string]*session.Session{}}
	m := newCmdTestModel(store)
	m.sess = &session.Session{ID: "s1"}

	// Create and bind. The cmd* methods return (tea.Model, tea.Cmd) and report
	// outcomes via the footer / session state, not an error return.
	m.cmdWorktreeNew("flowtest")
	if !strings.Contains(m.footerMessage, "Created and switched") {
		t.Fatalf("create footer = %q", m.footerMessage)
	}
	if m.sess.WorktreeDir == "" {
		t.Fatal("session not bound after new")
	}
	if store.updated == nil || store.updated.WorktreeDir == "" {
		t.Fatal("binding not persisted via store.Update")
	}
	// Process cwd should now be inside the worktree.
	if cwd, _ := os.Getwd(); !pathsEqual(cwd, m.sess.WorktreeDir) {
		t.Errorf("cwd %q not the worktree %q", cwd, m.sess.WorktreeDir)
	}

	// Footer segment should reflect the binding.
	seg := m.cachedWorktreeSegment()
	if !strings.Contains(seg, "flowtest") {
		t.Errorf("footer segment = %q, want contains worktree name", seg)
	}
	if !strings.Contains(seg, "detached@") {
		t.Errorf("footer segment = %q, want detached marker", seg)
	}

	// pwd reports the bound path.
	m.cmdWorktreePwd()

	// Modify a tracked file and confirm diff surfaces it.
	if err := os.WriteFile(filepath.Join(m.sess.WorktreeDir, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	m.invalidateWorktreeSegment()
	if seg := m.cachedWorktreeSegment(); !strings.Contains(seg, "±") {
		t.Errorf("dirty segment = %q, want dirty count", seg)
	}
	res, _ := m.cmdWorktreeDiff()
	rm := res.(*Model)
	if !strings.Contains(rm.dialog.Content(), "+world") {
		t.Errorf("diff dialog missing change:\n%s", rm.dialog.Content())
	}

	// Dirty remove without force must refuse.
	m.cmdWorktreeRemove(nil)
	if !strings.Contains(m.footerMessage, "uncommitted") {
		t.Errorf("dirty rm footer = %q, want refusal", m.footerMessage)
	}
	if m.sess.WorktreeDir == "" {
		t.Error("session should still be bound after refused remove")
	}

	// Forced remove succeeds and rebinds to root.
	m.cmdWorktreeRemove([]string{"force"})
	if m.sess.WorktreeDir != "" {
		t.Errorf("session still bound after force remove: %q", m.sess.WorktreeDir)
	}
	if cwd, _ := os.Getwd(); !pathsEqual(cwd, repo) {
		t.Errorf("cwd %q not back at root %q", cwd, repo)
	}
	if seg := m.cachedWorktreeSegment(); seg != "" {
		t.Errorf("segment after remove = %q, want empty", seg)
	}
}

// TestWorktreeSwitchToRootClearsBinding verifies "/worktree root" unbinds.
func TestWorktreeSwitchToRootClearsBinding(t *testing.T) {
	repo := initChatRepo(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))
	origWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	store := &mockStore{sessions: map[string]*session.Session{}}
	m := newCmdTestModel(store)
	m.sess = &session.Session{ID: "s1"}

	m.cmdWorktreeNew("rootswitch")
	if m.sess.WorktreeDir == "" {
		t.Fatalf("expected binding after new; footer = %q", m.footerMessage)
	}
	m.cmdWorktreeSwitch("root")
	if m.sess.WorktreeDir != "" {
		t.Errorf("binding not cleared after switch root: %q", m.sess.WorktreeDir)
	}
	if cwd, _ := os.Getwd(); !pathsEqual(cwd, repo) {
		t.Errorf("cwd %q not root %q", cwd, repo)
	}
	// Cleanup the created worktree so it doesn't linger in the repo metadata.
	t.Cleanup(func() {
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = repo
		_ = cmd.Run()
	})
}
