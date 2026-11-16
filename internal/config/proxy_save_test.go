package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveRefusesMaterializedProxyBeforeTouchingDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	path := filepath.Join(home, "term-llm", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("default_provider: openai\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Providers: map[string]ProviderConfig{"openai": {BaseURL: "https://proxy.example.test/openai", ProxyBinding: "https://proxy.example.test/openai", APIKey: "private-scoped-token"}}}
	if err := Save(cfg); err == nil || !strings.Contains(err.Error(), "materialized proxy") {
		t.Fatal("runtime proxy config must not be persisted", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("save changed source config", err)
	}
}
