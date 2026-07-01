package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWriteTarget(t *testing.T) {
	dir := t.TempDir()

	regular := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(regular, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := resolveWriteTarget(regular); got != regular {
		t.Fatalf("regular file resolved to %q", got)
	}

	missing := filepath.Join(dir, "missing.md")
	if got := resolveWriteTarget(missing); got != missing {
		t.Fatalf("missing path resolved to %q", got)
	}

	// Dangling symlinks resolve to their (not yet existing) target.
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink("target.md", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	want := filepath.Join(dir, "target.md")
	if got := resolveWriteTarget(link); got != want {
		t.Fatalf("dangling link resolved to %q, want %q", got, want)
	}

	// Chains resolve through intermediate links.
	link2 := filepath.Join(dir, "link2.md")
	if err := os.Symlink("link.md", link2); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if got := resolveWriteTarget(link2); got != want {
		t.Fatalf("chained link resolved to %q, want %q", got, want)
	}
}

// TestWriteFileToolWritesThroughSymlink ensures the atomic temp+rename write
// follows symlinks (including dangling ones) instead of replacing the link
// with a regular file. Renamed handover plan files rely on this.
func TestWriteFileToolWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "2026-07-02-amber-anchor-apple.md")
	if err := os.Symlink("2026-07-02-auth-refactor.md", link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	tool := NewWriteFileTool(nil)
	args, err := json.Marshal(WriteFileArgs{Path: link, Content: "the plan"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The link must survive and the content must land in its target.
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced (err %v)", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "2026-07-02-auth-refactor.md"))
	if err != nil || string(data) != "the plan" {
		t.Fatalf("target content = %q, err %v", data, err)
	}
}
