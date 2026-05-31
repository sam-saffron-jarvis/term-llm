package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSet_AtomicWriteFailureLeavesExistingConfigUntouched(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	configDir := filepath.Join(xdgHome, "term-llm")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	original := []byte("default_provider: anthropic\n")
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := os.Chmod(configDir, 0o500); err != nil {
		t.Fatalf("chmod config dir: %v", err)
	}
	defer os.Chmod(configDir, 0o755)

	err := configSet(nil, []string{"default_provider", "openai"})
	if err == nil {
		t.Skip("config directory remained writable; cannot exercise temp-file creation failure")
	}
	if !strings.Contains(err.Error(), "create temp file") && !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected atomic temp-file creation failure, got: %v", err)
	}

	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config after failed set: %v", readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("config was modified after failed atomic write:\n got: %q\nwant: %q", string(data), string(original))
	}
}

func TestConfigSet_AtomicWritePreservesExistingMode(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	configDir := filepath.Join(xdgHome, "term-llm")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("default_provider: anthropic\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := configSet(nil, []string{"default_provider", "openai"}); err != nil {
		t.Fatalf("config set: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mode 0600 to be preserved, got %o", got)
	}
}
