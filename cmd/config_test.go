package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
	"gopkg.in/yaml.v3"
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

func TestDefaultConfigContentContainsEveryDefault(t *testing.T) {
	content := defaultConfigContent()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("generated default config is not valid YAML: %v\n%s", err, content)
	}
	rawKeys := make(map[string]bool)
	unknownKeys := make(map[string]bool)
	extractConfigKeys(&root, "", rawKeys, unknownKeys)
	if len(unknownKeys) > 0 {
		t.Fatalf("generated default config contains unknown keys: %#v", unknownKeys)
	}
	for key := range config.GetDefaults() {
		if !rawKeys[key] {
			t.Fatalf("generated default config missing default key %s", key)
		}
	}
	for _, want := range []string{
		"# guardian:",
		"music:",
		"audio:",
		"reasoning:",
		"serve:",
		"file_tracking:",
		"max_tool_output_chars:",
		"strip_image_base64:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated default config missing %q", want)
		}
	}
}

func TestConfigResetWritesGeneratedDefaultsAtomically(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	if err := configReset(nil, nil); err != nil {
		t.Fatalf("config reset: %v", err)
	}
	configPath := filepath.Join(xdgHome, "term-llm", "config.yaml")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat reset config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("reset config mode = %o, want 0600", got)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read reset config: %v", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("reset config is not valid YAML: %v", err)
	}
	rawKeys := make(map[string]bool)
	unknownKeys := make(map[string]bool)
	extractConfigKeys(&root, "", rawKeys, unknownKeys)
	if len(unknownKeys) > 0 {
		t.Fatalf("reset config contains unknown keys: %#v", unknownKeys)
	}
	for _, key := range []string{"reasoning.display", "audio.gemini.model", "music.venice.model", "serve.response_timeout", "auto_compact"} {
		if !rawKeys[key] {
			t.Fatalf("reset config missing generated key %s", key)
		}
	}
}

func TestConfigShowRedactsSensitiveRawValues(t *testing.T) {
	raw := []byte(`providers:
  openai:
    api_key: sk-secret-test
    model: gpt-5.2
  claude-bin:
    env:
      CLAUDE_CODE_OAUTH_TOKEN: sk-ant-oat01-original
      IS_SANDBOX: "1"
  bedrock:
    secret_access_key: aws-secret-test
    session_token: aws-session-test
`)
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse raw config: %v", err)
	}
	rawKeys := make(map[string]bool)
	unknownKeys := make(map[string]bool)
	extractConfigKeys(&root, "", rawKeys, unknownKeys)
	var buf strings.Builder
	printAnnotatedConfig(&buf, config.GetDefaults(), rawKeys, unknownKeys, &root, true, nil)
	out := buf.String()
	for _, secret := range []string{"sk-secret-test", "sk-ant-oat01-original", "aws-secret-test", "aws-session-test"} {
		if strings.Contains(out, secret) {
			t.Fatalf("config output leaked sensitive value %q:\n%s", secret, out)
		}
	}
	if count := strings.Count(out, "<redacted>"); count < 4 {
		t.Fatalf("config output should redact sensitive values, got %d redactions:\n%s", count, out)
	}
	if !strings.Contains(out, "IS_SANDBOX: 1") {
		t.Fatalf("non-secret env key should still be rendered as config value? got:\n%s", out)
	}
}
