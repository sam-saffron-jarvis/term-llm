package mcp

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestManagerEnable_TimesOutStartupWithBackgroundContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}

	oldTimeout := mcpStartupTimeout
	mcpStartupTimeout = 100 * time.Millisecond
	defer func() { mcpStartupTimeout = oldTimeout }()

	manager := NewManager()
	manager.config = &Config{Servers: map[string]ServerConfig{
		"sleepy": {
			Command: "sh",
			Args:    []string{"-c", "sleep 10"},
		},
	}}
	defer manager.StopAll()

	if err := manager.Enable(context.Background(), "sleepy"); err != nil {
		t.Fatalf("Enable returned error: %v", err)
	}

	status, err := waitForServerStatus(t, manager, "sleepy", StatusFailed, 3*time.Second)
	if status != StatusFailed {
		t.Fatalf("status = %s, want %s", status, StatusFailed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("status error = %v, want wrapped context deadline exceeded", err)
	}
}

func TestManagerDisable_CancelsInFlightStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}

	oldTimeout := mcpStartupTimeout
	mcpStartupTimeout = 5 * time.Second
	defer func() { mcpStartupTimeout = oldTimeout }()

	manager := NewManager()
	manager.config = &Config{Servers: map[string]ServerConfig{
		"sleepy": {
			Command: "sh",
			Args:    []string{"-c", "sleep 10"},
		},
	}}
	defer manager.StopAll()

	enableCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Enable(enableCtx, "sleepy"); err != nil {
		t.Fatalf("Enable returned error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- manager.Disable("sleepy")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Disable returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		err := <-done
		t.Fatalf("Disable blocked waiting for startup to finish: %v", err)
	}

	status, err := manager.ServerStatus("sleepy")
	if status != StatusStopped {
		t.Fatalf("status immediately after Disable = %s, want %s", status, StatusStopped)
	}
	if err != nil {
		t.Fatalf("status error immediately after Disable = %v, want nil", err)
	}

	time.Sleep(200 * time.Millisecond)

	status, err = manager.ServerStatus("sleepy")
	if status != StatusStopped {
		t.Fatalf("status after canceled startup settled = %s, want %s", status, StatusStopped)
	}
	if err != nil {
		t.Fatalf("status error after canceled startup settled = %v, want nil", err)
	}
}

func waitForServerStatus(t *testing.T, manager *Manager, name string, want ServerStatus, timeout time.Duration) (ServerStatus, error) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := manager.ServerStatus(name)
		if status == want {
			return status, err
		}
		time.Sleep(10 * time.Millisecond)
	}

	status, err := manager.ServerStatus(name)
	t.Fatalf("timed out waiting for status %s; last status=%s err=%v", want, status, err)
	return status, err
}
