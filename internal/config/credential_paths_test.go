package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeCredentialsDirConfig(t *testing.T, name, text string) string {
	t.Helper()
	dir, err := GetConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeCredentialsDirYAMLSelection(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	custom, override := filepath.Join(home, "yaml-oauth"), filepath.Join(home, "env-oauth")
	for _, tc := range []struct {
		name, file, yaml, env, want string
		invalid                     bool
	}{
		{name: "absent"},
		{name: "empty", yaml: "credentials_dir: ''\n"},
		{name: "null", yaml: "credentials_dir: null\n"},
		{name: "YAML", yaml: "credentials_dir: " + custom, want: custom},
		{name: "alternate filename", file: "config.yml", yaml: "credentials_dir: " + custom, want: custom},
		{name: "home", yaml: "credentials_dir: ~/yaml-oauth", want: custom},
		{name: "case insensitive", yaml: "CREDENTIALS_DIR: " + custom, want: custom},
		{name: "merge alias", yaml: "paths: &paths {credentials_dir: " + custom + "}\n<<: *paths", want: custom},
		{name: "no provider decoding", yaml: "credentials_dir: " + custom + "\nproviders: [invalid-provider-shape]\napi_key: $(must-not-execute)", want: custom},
		{name: "env wins", yaml: "credentials_dir: " + custom, env: override, want: override},
		{name: "env ignores invalid directory", yaml: "credentials_dir: relative", env: override, want: override},
		{name: "env ignores invalid type", yaml: "credentials_dir: [secret-marker]", env: override, want: override},
		{name: "env skips malformed YAML", yaml: "providers: [secret-marker: {", env: override, want: override},
		{name: "relative", yaml: "credentials_dir: relative", invalid: true},
		{name: "no secret resolution", yaml: "credentials_dir: $(secret-marker)", invalid: true},
		{name: "list", yaml: "credentials_dir: [secret-marker]", invalid: true},
		{name: "number", yaml: "credentials_dir: 123", invalid: true},
		{name: "duplicate", yaml: "credentials_dir: ''\ncredentials_dir: secret-marker", invalid: true},
		{name: "case duplicate", yaml: "credentials_dir: ''\nCredentials_Dir: secret-marker", invalid: true},
		{name: "malformed", yaml: "providers: [secret-marker: {", invalid: true},
		{name: "invalid env cannot fall back", yaml: "credentials_dir: " + custom, env: "relative", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", tc.env)
			if tc.yaml != "" {
				name := tc.file
				if name == "" {
					name = "config.yaml"
				}
				writeCredentialsDirConfig(t, name, tc.yaml)
			}
			got, err := NativeCredentialsDir()
			if tc.invalid {
				if err == nil || strings.Contains(err.Error(), "secret-marker") {
					t.Fatalf("expected sanitized failure, got path=%q err=%v", got, err)
				}
				return
			}
			want := tc.want
			if want == "" {
				want, _ = GetConfigDir()
			}
			if err != nil || got != want {
				t.Fatalf("directory = %q, %v; want %q", got, err, want)
			}
		})
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatalf("resolution created the directory: %v", err)
	}
}

func TestCredentialsDirLoadAndSetupSave(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	path := writeCredentialsDirConfig(t, "config.yml", "credentials_dir: ~/creds/oauth\ndefault_provider: ollama\n")
	cfg, err := loadUnresolved()
	if err != nil || cfg.CredentialsDir != "~/creds/oauth" {
		t.Fatalf("loaded path = %+v, %v", cfg, err)
	}
	// Setup's fresh Config must preserve the literal YAML setting and must not
	// turn an environment-only override into a persisted configuration choice.
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", t.TempDir())
	if err := Save(&Config{DefaultProvider: "ollama"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCredentialsDir()
	if err != nil || got != "~/creds/oauth" {
		t.Fatalf("saved setting = %q, %v", got, err)
	}
	// config.yaml now takes precedence over the original config.yml.
	if err := os.WriteFile(path, []byte("credentials_dir: relative-invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCredentialsDir(); err != nil || got != "~/creds/oauth" {
		t.Fatalf("file priority changed: %q, %v", got, err)
	}
	cfg.CredentialsDir = t.TempDir()
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCredentialsDir(); err != nil || got != cfg.CredentialsDir {
		t.Fatalf("explicit save lost field: %q, %v", got, err)
	}
	if !IsKnownKey("credentials_dir") {
		t.Fatal("credentials_dir missing from config schema")
	}
}

func TestCredentialsDirLoadRejectsInvalidUnlessOverridden(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, value := range []string{"relative", "[secret-marker]"} {
		writeCredentialsDirConfig(t, "config.yaml", "credentials_dir: "+value)
		t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
		if _, err := loadUnresolved(); err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("expected sanitized invalid path error: %v", err)
		}
		t.Setenv("TERM_LLM_CREDENTIALS_DIR", t.TempDir())
		if _, err := loadUnresolved(); err != nil {
			t.Fatalf("environment did not override unused YAML value: %v", err)
		}
	}
}
