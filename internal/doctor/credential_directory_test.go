package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestCredentialsCheckDirectoryOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	writeCredFile(t, root, "chatgpt_oauth.json", map[string]any{"access_token": "default-fixture"}, 0o600)
	custom := filepath.Join(t.TempDir(), "oauth")
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	if err := os.WriteFile(filepath.Join(root, "term-llm", "config.yaml"), []byte(fmt.Sprintf("credentials_dir: %q\n", custom)), 0o600); err != nil {
		t.Fatal(err)
	}
	check := CredentialsCheck{Config: &config.Config{DefaultProvider: "chatgpt"}}
	findings := check.Run(context.Background())
	missing := credentialsFindingWithTitle(t, findings, "chatgpt: not signed in")
	if !strings.Contains(missing.Detail, custom) {
		t.Fatalf("doctor checked the wrong directory: %+v", missing)
	}
	if err := os.MkdirAll(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(custom, "chatgpt_oauth.json")
	if err := os.WriteFile(path, []byte("invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	findings = check.Run(context.Background())
	credentialsFindingWithTitle(t, findings, "chatgpt: credential file is unusable")
	if err := os.WriteFile(path, []byte(`{"access_token":"custom-fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsFindingWithTitle(t, check.Run(context.Background()), "chatgpt: signed in")
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "relative")
	finding := credentialsFindingWithTitle(t, check.Run(context.Background()), "chatgpt: cannot locate credential file")
	if finding.Severity != SeverityError {
		t.Fatalf("invalid override severity = %s", finding.Severity)
	}
}
