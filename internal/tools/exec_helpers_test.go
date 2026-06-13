package tools

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestResolveToolPath_TildeExpanded verifies that resolveToolPath correctly
// expands a bare ~ to the user's home directory.
func TestResolveToolPath_TildeExpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	got, err := resolveToolPath("~", false)
	if err != nil {
		t.Fatalf("resolveToolPath(~) returned error: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("resolveToolPath(~) = %q, expected prefix %q", got, home)
	}
}

// TestResolveToolPath_TildeSlashExpanded verifies ~/subpath expansion.
func TestResolveToolPath_TildeSlashExpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	// Use a subpath that is guaranteed to exist so canonicalizePath doesn't fail.
	got, err := resolveToolPath("~/", false)
	if err != nil {
		t.Fatalf("resolveToolPath(~/) returned error: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("resolveToolPath(~/) = %q, expected prefix %q", got, home)
	}
}

func TestShouldScanTaggedDescendants(t *testing.T) {
	tests := []struct {
		name          string
		opts          toolCommandCleanupOptions
		cancelCalled  bool
		pgroupKillErr error
		want          bool
	}{
		{
			name: "default helper keeps always-scan behavior",
			opts: toolCommandCleanupOptions{alwaysScanTaggedDescendants: true},
			want: true,
		},
		{
			name:          "skip scan for clean foreground shell exit",
			opts:          toolCommandCleanupOptions{},
			pgroupKillErr: syscall.ESRCH,
			want:          false,
		},
		{
			name:          "scan when command was cancelled or timed out",
			opts:          toolCommandCleanupOptions{},
			cancelCalled:  true,
			pgroupKillErr: syscall.ESRCH,
			want:          true,
		},
		{
			name:          "scan when pgroup kill found lingering processes",
			opts:          toolCommandCleanupOptions{},
			pgroupKillErr: nil,
			want:          true,
		},
		{
			name:          "scan when caller suspects escaped background descendants",
			opts:          toolCommandCleanupOptions{suspectBackgroundDescendants: true},
			pgroupKillErr: syscall.ESRCH,
			want:          true,
		},
		{
			name:          "scan on unexpected kill error to stay safe",
			opts:          toolCommandCleanupOptions{},
			pgroupKillErr: errors.New("boom"),
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldScanTaggedDescendants(tt.opts, tt.cancelCalled, tt.pgroupKillErr)
			if got != tt.want {
				t.Fatalf("shouldScanTaggedDescendants(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShellCommandMayLeaveBackgroundDescendants(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "pwd", want: false},
		{command: "git status && pwd", want: false},
		{command: "grep foo file 2>&1", want: false},
		{command: "echo '&'", want: false},
		{command: "sleep 1 &", want: true},
		{command: "nohup sleep 1 >/tmp/x 2>&1 &", want: true},
		{command: "setsid bash -c 'sleep 1' >/tmp/x 2>&1 < /dev/null &", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := shellCommandMayLeaveBackgroundDescendants(tt.command)
			if got != tt.want {
				t.Fatalf("shellCommandMayLeaveBackgroundDescendants(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
