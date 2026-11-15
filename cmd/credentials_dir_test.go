package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestConfigCredentialsDirGetSetAndTemplate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	if err := configSet(nil, []string{"credentials_dir", "~/creds/oauth"}); err != nil {
		t.Fatal(err)
	}
	var getErr error
	out := captureStdout(t, func() { getErr = configGet(nil, []string{"credentials_dir"}) })
	if getErr != nil || strings.TrimSpace(out) != "~/creds/oauth" {
		t.Fatalf("config get = %q, %v", out, getErr)
	}
	if !strings.Contains(defaultConfigContent(), "credentials_dir:") || !config.IsKnownKey("credentials_dir") {
		t.Fatal("credentials_dir missing from schema/reset template")
	}
	if err := configSet(nil, []string{"credentials_dir", "relative"}); err == nil {
		t.Fatal("config set accepted a relative credential directory")
	}
	if raw, err := config.ReadCredentialsDir(); err != nil || raw != "~/creds/oauth" {
		t.Fatalf("rejected edit changed config: %q, %v", raw, err)
	}
	if err := configSet(nil, []string{"credentials_dir", ""}); err != nil {
		t.Fatal(err)
	}
	if raw, err := config.ReadCredentialsDir(); err != nil || raw != "" {
		t.Fatalf("could not clear credentials_dir: %q, %v", raw, err)
	}
}

func TestConfigCredentialsDirEditsSelectedYML(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	dir, err := config.GetConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(selected, []byte("default_provider: ollama\nproviders:\n  ollama:\n    model: fixture-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{t.TempDir(), ""} {
		if err := configSet(nil, []string{"credentials_dir", value}); err != nil {
			t.Fatal(err)
		}
		if got, err := config.FindConfigFile(); err != nil || got != selected {
			t.Fatalf("selected config = %q, %v; want original config.yml", got, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
			t.Fatalf("setter created a shadow config.yaml: %v", err)
		}
		cfg, err := config.Load()
		if err != nil || cfg.DefaultProvider != "ollama" || cfg.Providers["ollama"].Model != "fixture-model" || cfg.CredentialsDir != value {
			t.Fatalf("effective settings changed: %+v, %v", cfg, err)
		}
	}
}
