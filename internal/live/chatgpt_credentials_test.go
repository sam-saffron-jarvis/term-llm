package live

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/credentials"
)

func TestChatGPTAuthCredentialDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	creds := &credentials.ChatGPTCredentials{AccessToken: "default-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := credentials.SaveChatGPTCredentials(creds); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "oauth")
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm", "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("credentials_dir: %q\n", custom)), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := NewChatGPTProvider(config.LiveConfig{}, nil, nil)
	if provider.Ready(context.Background()) == nil {
		t.Fatal("readiness fell back to default credentials")
	}
	if _, err := ChatGPTAuth(context.Background(), false); err == nil {
		t.Fatal("live auth fell back to default credentials")
	}
	creds.AccessToken = "custom-fixture"
	creds.AccountID = "custom-account"
	if err := credentials.SaveChatGPTCredentials(creds); err != nil {
		t.Fatal(err)
	}
	if err := provider.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	auth, err := ChatGPTAuth(context.Background(), false)
	if err != nil || auth.AccessToken != creds.AccessToken || auth.AccountID != creds.AccountID {
		t.Fatalf("auth = %+v, %v", auth, err)
	}
	path, err := credentials.ChatGPTCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if provider.Ready(context.Background()) == nil {
		t.Fatal("readiness accepted corrupt custom credentials")
	}
}
