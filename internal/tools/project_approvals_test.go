package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectApprovals_EmptyRoot(t *testing.T) {
	pa, err := LoadProjectApprovals("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if pa != nil {
		t.Error("expected nil for empty root")
	}
}

func TestLoadProjectApprovals_NewProject(t *testing.T) {
	// Use a temp directory as the "repo root"
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Override XDG_CONFIG_HOME to use temp directory for storage
	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pa == nil {
		t.Fatal("expected non-nil ProjectApprovals")
	}

	// Check defaults
	if pa.IsReadApproved() {
		t.Error("new project should not have read approved")
	}
	if pa.IsWriteApproved() {
		t.Error("new project should not have write approved")
	}
	if pa.RepoRoot != tempDir {
		t.Errorf("expected RepoRoot=%s, got %s", tempDir, pa.RepoRoot)
	}
	if pa.RepoName != filepath.Base(tempDir) {
		t.Errorf("expected RepoName=%s, got %s", filepath.Base(tempDir), pa.RepoName)
	}
}

func TestProjectApprovals_ReadApproval(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Approve read
	if err := pa.ApproveRead(); err != nil {
		t.Fatalf("ApproveRead failed: %v", err)
	}

	if !pa.IsReadApproved() {
		t.Error("expected read to be approved")
	}
	if pa.IsWriteApproved() {
		t.Error("write should not be approved")
	}

	// Reload and verify persistence
	pa2, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !pa2.IsReadApproved() {
		t.Error("read approval should persist after reload")
	}
}

func TestProjectApprovals_WriteApproval(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Approve write
	if err := pa.ApproveWrite(); err != nil {
		t.Fatalf("ApproveWrite failed: %v", err)
	}

	if !pa.IsWriteApproved() {
		t.Error("expected write to be approved")
	}
	if pa.IsReadApproved() {
		t.Error("read should not be approved (only write was approved)")
	}

	// Reload and verify persistence
	pa2, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !pa2.IsWriteApproved() {
		t.Error("write approval should persist after reload")
	}
}

func TestProjectApprovals_IsPathApproved(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	testFile := filepath.Join(tempDir, "src", "main.go")

	// Initially not approved
	if pa.IsPathApproved(testFile, false) {
		t.Error("path should not be approved initially")
	}

	// Approve read for entire repo
	if err := pa.ApproveRead(); err != nil {
		t.Fatalf("ApproveRead failed: %v", err)
	}

	// Now should be approved for read
	if !pa.IsPathApproved(testFile, false) {
		t.Error("path should be approved for read after repo approval")
	}

	// But not for write
	if pa.IsPathApproved(testFile, true) {
		t.Error("path should not be approved for write (only read was approved)")
	}

	// Approve write
	if err := pa.ApproveWrite(); err != nil {
		t.Fatalf("ApproveWrite failed: %v", err)
	}

	// Now should be approved for write too
	if !pa.IsPathApproved(testFile, true) {
		t.Error("path should be approved for write after write approval")
	}
}

func TestProjectApprovals_IsPathApproved_IndividualPaths(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Approve a specific directory
	srcDir := filepath.Join(tempDir, "src")
	if err := pa.ApprovePath(srcDir); err != nil {
		t.Fatalf("ApprovePath failed: %v", err)
	}

	// File in approved directory should be approved
	if !pa.IsPathApproved(filepath.Join(srcDir, "main.go"), false) {
		t.Error("file in approved directory should be approved")
	}

	// File outside approved directory should not be approved
	if pa.IsPathApproved(filepath.Join(tempDir, "other", "file.go"), false) {
		t.Error("file outside approved directory should not be approved")
	}

	// Verify persistence
	pa2, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !pa2.IsPathApproved(filepath.Join(srcDir, "main.go"), false) {
		t.Error("path approval should persist after reload")
	}
}

func TestProjectApprovals_ShellPatterns(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Initially no patterns approved
	if pa.IsShellPatternApproved("go test ./...") {
		t.Error("command should not be approved initially")
	}

	// Approve a pattern
	if err := pa.ApproveShellPattern("go test *"); err != nil {
		t.Fatalf("ApproveShellPattern failed: %v", err)
	}

	// Matching command should be approved
	if !pa.IsShellPatternApproved("go test ./...") {
		t.Error("matching command should be approved")
	}

	// Non-matching command should not be approved
	if pa.IsShellPatternApproved("go build ./...") {
		t.Error("non-matching command should not be approved")
	}

	// Verify persistence
	pa2, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !pa2.IsShellPatternApproved("go test ./...") {
		t.Error("shell pattern should persist after reload")
	}
}

func TestProjectApprovals_NilSafe(t *testing.T) {
	var pa *ProjectApprovals

	// All methods should be safe to call on nil
	if pa.IsReadApproved() {
		t.Error("nil.IsReadApproved should return false")
	}
	if pa.IsWriteApproved() {
		t.Error("nil.IsWriteApproved should return false")
	}
	if pa.IsPathApproved("/some/path", false) {
		t.Error("nil.IsPathApproved should return false")
	}
	if pa.IsShellPatternApproved("some command") {
		t.Error("nil.IsShellPatternApproved should return false")
	}

	// These should not panic
	_ = pa.ApproveRead()
	_ = pa.ApproveWrite()
	_ = pa.ApprovePath("/some/path")
	_ = pa.ApproveShellPattern("some pattern")
	_ = pa.Save()
}

func TestProjectApprovals_DuplicateApprovals(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Approve same path multiple times
	path := filepath.Join(tempDir, "src")
	if err := pa.ApprovePath(path); err != nil {
		t.Fatalf("ApprovePath failed: %v", err)
	}
	if err := pa.ApprovePath(path); err != nil {
		t.Fatalf("ApprovePath (duplicate) failed: %v", err)
	}

	// Should only have one entry
	if len(pa.ApprovedPaths) != 1 {
		t.Errorf("expected 1 approved path, got %d", len(pa.ApprovedPaths))
	}

	// Same for shell patterns
	if err := pa.ApproveShellPattern("go test *"); err != nil {
		t.Fatalf("ApproveShellPattern failed: %v", err)
	}
	if err := pa.ApproveShellPattern("go test *"); err != nil {
		t.Fatalf("ApproveShellPattern (duplicate) failed: %v", err)
	}

	if len(pa.ShellPatterns) != 1 {
		t.Errorf("expected 1 shell pattern, got %d", len(pa.ShellPatterns))
	}
}

func TestGenerateShellPattern(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"go test ./...", "go test *"},
		{"npm install lodash", "npm install *"},
		{"git status", "git status *"},
		{"make", "make"},
		{"python script.py arg1 arg2", "python *"},
		{"ls", "ls"},
		{"ls -la", "ls *"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := GenerateShellPattern(tt.command)
			if got != tt.want {
				t.Errorf("GenerateShellPattern(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestProjectApprovals_StorageLocation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Save something
	if err := pa.ApproveRead(); err != nil {
		t.Fatalf("ApproveRead failed: %v", err)
	}

	// Check that file was created in expected location
	projectsDir := filepath.Join(configDir, "term-llm", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		t.Fatalf("failed to read projects dir: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 file in projects dir, got %d", len(entries))
	}

	// File should have .yaml extension
	if len(entries) > 0 && filepath.Ext(entries[0].Name()) != ".yaml" {
		t.Errorf("expected .yaml file, got %s", entries[0].Name())
	}
}
