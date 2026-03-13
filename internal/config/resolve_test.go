package config

import (
	"strings"
	"testing"
	"time"
)

func TestResolveCommand_TimesOutAndKillsProcessGroup(t *testing.T) {
	origTimeout := resolveExecTimeout
	origWaitDelay := resolveExecWaitDelay
	resolveExecTimeout = 50 * time.Millisecond
	resolveExecWaitDelay = 50 * time.Millisecond
	defer func() {
		resolveExecTimeout = origTimeout
		resolveExecWaitDelay = origWaitDelay
	}()

	start := time.Now()
	_, err := resolveCommand("sleep 2 & wait")
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q, want timeout message", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("resolveCommand took %s after timeout, want under 500ms", elapsed)
	}
}

func TestResolveCommand_RejectsOversizedOutput(t *testing.T) {
	origLimit := resolveExecOutputLimit
	resolveExecOutputLimit = 32
	defer func() {
		resolveExecOutputLimit = origLimit
	}()

	_, err := resolveCommand("i=0; while [ $i -lt 200 ]; do printf x; i=$((i+1)); done")
	if err == nil {
		t.Fatalf("expected output limit error")
	}
	if !strings.Contains(err.Error(), "output exceeded 32 bytes") {
		t.Fatalf("error = %q, want output limit message", err)
	}
}
