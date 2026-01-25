package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		command string
		want    bool
	}{
		// Exact matches
		{"git status", "git status", true},
		{"npm test", "npm test", true},
		{"ls", "ls", true},

		// Wildcard patterns
		{"git *", "git status", true},
		{"git *", "git commit -m 'message'", true},
		{"go test *", "go test ./...", true},
		{"npm *", "npm install lodash", true},

		// Non-matches
		{"git *", "npm install", false},
		{"git status", "git commit", false},
		{"npm test", "npm install", false},
		{"", "anything", false},

		// Edge cases
		{"*", "anything", true},
		{"a*", "abc", true},
		{"a*", "bcd", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.command, func(t *testing.T) {
			got := matchPattern(tt.pattern, tt.command)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.command, got, tt.want)
			}
		})
	}
}

func TestApprovalManager_CheckPathApproval_PreApproved(t *testing.T) {
	// Create temp directory structure
	tempDir, err := os.MkdirTemp("", "test-approval-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	readDir := filepath.Join(tempDir, "allowed-read")
	writeDir := filepath.Join(tempDir, "allowed-write")
	if err := os.MkdirAll(readDir, 0755); err != nil {
		t.Fatalf("failed to create read dir: %v", err)
	}
	if err := os.MkdirAll(writeDir, 0755); err != nil {
		t.Fatalf("failed to create write dir: %v", err)
	}

	// Create test files (permissions check validates file exists)
	readFile := filepath.Join(readDir, "file.txt")
	writeFile := filepath.Join(writeDir, "file.txt")
	if err := os.WriteFile(readFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create read file: %v", err)
	}
	if err := os.WriteFile(writeFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create write file: %v", err)
	}

	// Create permissions with pre-approved directories
	perms := NewToolPermissions()
	perms.ReadDirs = []string{readDir}
	perms.WriteDirs = []string{writeDir}

	mgr := NewApprovalManager(perms)

	// Test read access to pre-approved read directory
	outcome, err := mgr.CheckPathApproval("read_file", readFile, "", false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedOnce {
		t.Errorf("expected ProceedOnce for pre-approved read, got %v", outcome)
	}

	// Test write access to pre-approved write directory
	outcome, err = mgr.CheckPathApproval("write_file", writeFile, "", true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedOnce {
		t.Errorf("expected ProceedOnce for pre-approved write, got %v", outcome)
	}

	// Note: In this implementation, WriteDirs do NOT automatically include read permission.
	// Read access must be explicitly granted via ReadDirs.
}

func TestApprovalManager_CheckPathApproval_SessionCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-approval-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Resolve symlinks (macOS /var -> /private/var)
	tempDir, err = filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("failed to resolve symlinks: %v", err)
	}

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	testDir := filepath.Join(tempDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// Create test file
	testFile := filepath.Join(testDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Manually add directory to session cache
	mgr.dirCache.Set(testDir, ProceedAlways)

	// Check should succeed without prompting
	outcome, err := mgr.CheckPathApproval("read_file", testFile, "", false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedAlways {
		t.Errorf("expected ProceedAlways from session cache, got %v", outcome)
	}
}

func TestApprovalManager_CheckPathApproval_ProjectApprovals(t *testing.T) {
	// Create a git repo for testing
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Resolve symlinks (macOS /var -> /private/var)
	tempDir, err = filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("failed to resolve symlinks: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed, skipping: %v", err)
	}

	// Set up config dir
	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	// Create test file
	testFile := filepath.Join(tempDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Pre-approve the repo
	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("failed to load project approvals: %v", err)
	}
	if err := pa.ApproveRead(); err != nil {
		t.Fatalf("failed to approve read: %v", err)
	}

	// Check should succeed from project approvals
	outcome, err := mgr.CheckPathApproval("read_file", testFile, "", false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedAlways {
		t.Errorf("expected ProceedAlways from project approvals, got %v", outcome)
	}
}

func TestApprovalManager_CheckShellApproval_PreApproved(t *testing.T) {
	perms := NewToolPermissions()
	perms.ShellAllow = []string{"git *", "go test *"}
	if err := perms.CompileShellPatterns(); err != nil {
		t.Fatalf("failed to compile patterns: %v", err)
	}

	mgr := NewApprovalManager(perms)

	// Test matching pattern
	outcome, err := mgr.CheckShellApproval("git status")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedOnce {
		t.Errorf("expected ProceedOnce for pre-approved command, got %v", outcome)
	}

	outcome, err = mgr.CheckShellApproval("go test ./...")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedOnce {
		t.Errorf("expected ProceedOnce for pre-approved command, got %v", outcome)
	}
}

func TestApprovalManager_CheckShellApproval_SessionCache(t *testing.T) {
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	// Add pattern to session cache
	mgr.shellCache.AddPattern("npm *")

	// Check should succeed without prompting
	outcome, err := mgr.CheckShellApproval("npm install lodash")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedAlways {
		t.Errorf("expected ProceedAlways from session cache, got %v", outcome)
	}
}

func TestApprovalManager_CheckShellApproval_ProjectApprovals(t *testing.T) {
	// Create a git repo for testing
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Resolve symlinks (macOS /var -> /private/var)
	tempDir, err = filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("failed to resolve symlinks: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed, skipping: %v", err)
	}

	// Set up config dir
	configDir, err := os.MkdirTemp("", "test-config-*")
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)

	oldXDG := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", configDir)
	defer os.Setenv("XDG_CONFIG_HOME", oldXDG)

	// Change to repo dir so shell approval detects it
	oldCwd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldCwd)

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	// Pre-approve shell pattern in project
	pa, err := LoadProjectApprovals(tempDir)
	if err != nil {
		t.Fatalf("failed to load project approvals: %v", err)
	}
	if err := pa.ApproveShellPattern("make *"); err != nil {
		t.Fatalf("failed to approve pattern: %v", err)
	}

	// Check should succeed from project approvals
	outcome, err := mgr.CheckShellApproval("make build")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if outcome != ProceedAlways {
		t.Errorf("expected ProceedAlways from project approvals, got %v", outcome)
	}
}

func TestApprovalManager_CheckPathApproval_NoPromptFunc(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-approval-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)
	// Don't set PromptFunc or PromptUIFunc

	// Should return error when path is not pre-approved
	outcome, err := mgr.CheckPathApproval("read_file", filepath.Join(tempDir, "file.txt"), "", false)
	if err == nil {
		t.Error("expected error when no prompt func set")
	}
	if outcome != Cancel {
		t.Errorf("expected Cancel outcome, got %v", outcome)
	}
}

func TestApprovalManager_CheckShellApproval_NoPromptFunc(t *testing.T) {
	// Create temp directory and change to it to avoid picking up project approvals
	tempDir, err := os.MkdirTemp("", "test-no-prompt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldCwd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldCwd)

	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)
	// Don't set PromptFunc or PromptUIFunc

	// Should return error when command is not pre-approved
	outcome, err := mgr.CheckShellApproval("rm -rf /")
	if err == nil {
		t.Error("expected error when no prompt func set")
	}
	if outcome != Cancel {
		t.Errorf("expected Cancel outcome, got %v", outcome)
	}
}

func TestApprovalManager_HandleFileApprovalResult(t *testing.T) {
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	tempDir, err := os.MkdirTemp("", "test-handle-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name    string
		result  ApprovalResult
		want    ConfirmOutcome
		wantErr bool
	}{
		{
			name:    "cancelled",
			result:  ApprovalResult{Cancelled: true},
			want:    Cancel,
			wantErr: false,
		},
		{
			name:    "deny",
			result:  ApprovalResult{Choice: ApprovalChoiceDeny},
			want:    Cancel,
			wantErr: false,
		},
		{
			name:    "once",
			result:  ApprovalResult{Choice: ApprovalChoiceOnce},
			want:    ProceedOnce,
			wantErr: false,
		},
		{
			name:    "file",
			result:  ApprovalResult{Choice: ApprovalChoiceFile},
			want:    ProceedOnce,
			wantErr: false,
		},
		{
			name:    "directory",
			result:  ApprovalResult{Choice: ApprovalChoiceDirectory, Path: tempDir},
			want:    ProceedAlways,
			wantErr: false,
		},
		{
			name:    "repo_read",
			result:  ApprovalResult{Choice: ApprovalChoiceRepoRead, Path: tempDir},
			want:    ProceedAlways,
			wantErr: false,
		},
		{
			name:    "repo_write",
			result:  ApprovalResult{Choice: ApprovalChoiceRepoWrite, Path: tempDir},
			want:    ProceedAlways,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := mgr.handleFileApprovalResult(tt.result, tempDir, false, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleFileApprovalResult() error = %v, wantErr %v", err, tt.wantErr)
			}
			if outcome != tt.want {
				t.Errorf("handleFileApprovalResult() = %v, want %v", outcome, tt.want)
			}
		})
	}
}

func TestApprovalManager_HandleShellApprovalResult(t *testing.T) {
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	tests := []struct {
		name    string
		result  ApprovalResult
		want    ConfirmOutcome
		wantErr bool
	}{
		{
			name:    "cancelled",
			result:  ApprovalResult{Cancelled: true},
			want:    Cancel,
			wantErr: false,
		},
		{
			name:    "deny",
			result:  ApprovalResult{Choice: ApprovalChoiceDeny},
			want:    Cancel,
			wantErr: false,
		},
		{
			name:    "once",
			result:  ApprovalResult{Choice: ApprovalChoiceOnce},
			want:    ProceedOnce,
			wantErr: false,
		},
		{
			name:    "command",
			result:  ApprovalResult{Choice: ApprovalChoiceCommand},
			want:    ProceedAlways,
			wantErr: false,
		},
		{
			name:    "pattern",
			result:  ApprovalResult{Choice: ApprovalChoicePattern, Pattern: "git *"},
			want:    ProceedAlways,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := mgr.handleShellApprovalResult(tt.result, "git status", nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleShellApprovalResult() error = %v, wantErr %v", err, tt.wantErr)
			}
			if outcome != tt.want {
				t.Errorf("handleShellApprovalResult() = %v, want %v", outcome, tt.want)
			}
		})
	}
}

func TestApprovalManager_HandleFileApprovalResult_Directory_AddedToCache(t *testing.T) {
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	tempDir, err := os.MkdirTemp("", "test-cache-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	result := ApprovalResult{
		Choice: ApprovalChoiceDirectory,
		Path:   tempDir,
	}

	_, err = mgr.handleFileApprovalResult(result, tempDir, false, nil)
	if err != nil {
		t.Fatalf("handleFileApprovalResult failed: %v", err)
	}

	// Verify directory was added to session cache
	if !mgr.dirCache.IsPathInApprovedDir(filepath.Join(tempDir, "subdir", "file.txt")) {
		t.Error("directory should be in approved cache after approval")
	}
}

func TestApprovalManager_HandleShellApprovalResult_Pattern_AddedToCache(t *testing.T) {
	perms := NewToolPermissions()
	mgr := NewApprovalManager(perms)

	result := ApprovalResult{
		Choice:  ApprovalChoicePattern,
		Pattern: "cargo *",
	}

	_, err := mgr.handleShellApprovalResult(result, "cargo build", nil)
	if err != nil {
		t.Fatalf("handleShellApprovalResult failed: %v", err)
	}

	// Verify pattern was added to session cache
	patterns := mgr.shellCache.GetPatterns()
	found := false
	for _, p := range patterns {
		if p == "cargo *" {
			found = true
			break
		}
	}
	if !found {
		t.Error("pattern should be in session cache after approval")
	}
}

func TestDirCache_IsPathInApprovedDir(t *testing.T) {
	cache := NewDirCache()

	// Add approved directories
	cache.Set("/home/user/project", ProceedAlways)
	cache.Set("/tmp/allowed", ProceedAlways)

	tests := []struct {
		path string
		want bool
	}{
		{"/home/user/project/src/main.go", true},
		{"/home/user/project", true},
		{"/home/user/other/file.go", false},
		{"/tmp/allowed/subdir/file", true},
		{"/tmp/other", false},
		{"/home/user/project-extra/file", false}, // Similar prefix but different dir
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := cache.IsPathInApprovedDir(tt.path)
			if got != tt.want {
				t.Errorf("IsPathInApprovedDir(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestShellApprovalCache(t *testing.T) {
	cache := NewShellApprovalCache()

	// Initially empty
	if len(cache.GetPatterns()) != 0 {
		t.Error("new cache should be empty")
	}

	// Add patterns
	cache.AddPattern("git *")
	cache.AddPattern("npm test")
	cache.AddPattern("git *") // Duplicate

	patterns := cache.GetPatterns()
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns (no duplicates), got %d", len(patterns))
	}

	// Verify patterns are present
	hasGit, hasNpm := false, false
	for _, p := range patterns {
		if p == "git *" {
			hasGit = true
		}
		if p == "npm test" {
			hasNpm = true
		}
	}
	if !hasGit || !hasNpm {
		t.Error("expected both patterns to be present")
	}
}
