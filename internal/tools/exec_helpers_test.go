package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestPrepareToolCommand_SkipsTaggedSweepForForegroundCommand(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	origReaper := taggedDescendantReaper
	t.Cleanup(func() { taggedDescendantReaper = origReaper })

	sweeps := 0
	taggedDescendantReaper = func(nonce string) {
		sweeps++
		origReaper(nonce)
	}

	cmd := exec.CommandContext(context.Background(), "bash", "-c", "pwd >/dev/null")
	cleanup, err := prepareToolCommand(cmd, false)
	if err != nil {
		t.Fatalf("prepareToolCommand returned error: %v", err)
	}

	if err := cmd.Run(); err != nil {
		cleanup()
		t.Fatalf("cmd.Run returned error: %v", err)
	}
	cleanup()

	if sweeps != 0 {
		t.Fatalf("tagged descendant sweep ran %d times for a foreground command, want 0", sweeps)
	}
}

func TestPrepareToolCommand_SweepsTaggedDescendantsWhenDetachedChildSurvives(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not available")
	}
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc — nonce-based descendant reap is Linux-only")
	}

	origReaper := taggedDescendantReaper
	t.Cleanup(func() { taggedDescendantReaper = origReaper })

	sweeps := 0
	taggedDescendantReaper = func(nonce string) {
		sweeps++
		origReaper(nonce)
	}

	sentinel := uniqueSentinel(t, "probe")
	logPath := fmt.Sprintf("/tmp/%s.log", sentinel)
	defer os.Remove(logPath)

	cmd := exec.CommandContext(
		context.Background(),
		"bash",
		"-c",
		fmt.Sprintf("setsid bash -c 'sleep 120; :%s' >%s 2>&1 < /dev/null & echo ok", sentinel, logPath),
	)
	cleanup, err := prepareToolCommand(cmd, true)
	if err != nil {
		t.Fatalf("prepareToolCommand returned error: %v", err)
	}

	if err := cmd.Run(); err != nil {
		cleanup()
		t.Fatalf("cmd.Run returned error: %v", err)
	}
	cleanup()

	if sweeps == 0 {
		t.Fatal("expected nonce-based descendant sweep for detached child, got 0 sweeps")
	}

	time.Sleep(250 * time.Millisecond)
	found, _ := exec.Command("pgrep", "-f", sentinel).Output()
	if stray := strings.TrimSpace(string(found)); stray != "" {
		_ = exec.Command("pkill", "-f", sentinel).Run()
		t.Fatalf("detached sentinel process still alive after cleanup: %q", stray)
	}
}
