package tools

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
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

func TestRequiresTaggedSweep_NoInheritedWriters(t *testing.T) {
	cmd := exec.Command("true")
	probe, err := newDescendantLeakProbe(cmd)
	if err != nil {
		t.Fatalf("newDescendantLeakProbe() error = %v", err)
	}
	defer probe.close()

	if requiresTaggedSweep(probe) {
		t.Fatal("requiresTaggedSweep() = true, want false when no descendant inherited the probe fd")
	}
}

func TestRequiresTaggedSweep_WithInheritedWriter(t *testing.T) {
	cmd := exec.Command("true")
	probe, err := newDescendantLeakProbe(cmd)
	if err != nil {
		t.Fatalf("newDescendantLeakProbe() error = %v", err)
	}
	defer probe.close()

	dupFD, err := syscall.Dup(int(probe.writer.Fd()))
	if err != nil {
		t.Fatalf("Dup() error = %v", err)
	}
	childWriter := os.NewFile(uintptr(dupFD), "probe-child-writer")
	defer childWriter.Close()

	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = childWriter.Close()
	}()

	if !requiresTaggedSweep(probe) {
		t.Fatal("requiresTaggedSweep() = false, want true while a descendant-style writer is still open")
	}
}
