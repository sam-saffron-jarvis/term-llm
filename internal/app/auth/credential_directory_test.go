package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/credentials"
)

func TestAuthCredentialDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	if err := credentials.SaveChatGPTCredentials(&credentials.ChatGPTCredentials{AccessToken: "default-fixture"}); err != nil {
		t.Fatal(err)
	}
	defaultPath, err := credentials.ChatGPTCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", filepath.Join(t.TempDir(), "oauth"))
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := RunStatus(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "signed in;") {
		t.Fatalf("status fell back to default credentials: %s", output.String())
	}
	if err := credentials.SaveChatGPTCredentials(&credentials.ChatGPTCredentials{
		AccessToken: "custom-fixture", AccountID: "custom-account", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := RunStatus(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "custom-account") || !strings.Contains(output.String(), "signed in;") {
		t.Fatalf("status missed custom credentials: %s", output.String())
	}
	if err := RunLogout(cmd, []string{"chatgpt"}); err != nil {
		t.Fatal(err)
	}
	if credentials.ChatGPTCredentialsExist() {
		t.Fatal("logout left custom credentials")
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatalf("logout changed default credentials: %v", err)
	}
}
