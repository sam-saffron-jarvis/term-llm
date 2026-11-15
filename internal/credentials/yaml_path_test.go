package credentials

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/oauth"
)

func selectYAMLCredentialDir(t *testing.T, dir string) {
	t.Helper()
	configDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(fmt.Sprintf("credentials_dir: %q\n", dir)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCredentialStoresYAML(t *testing.T) {
	for _, store := range nativeCredentialStores() {
		t.Run(store.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			defaultPath, _ := store.path()
			original, err := os.ReadFile(defaultPath)
			if err != nil {
				t.Fatal(err)
			}
			custom := filepath.Join(t.TempDir(), "oauth")
			selectYAMLCredentialDir(t, custom)
			if store.exists() || store.load() == nil {
				t.Fatal("missing YAML store fell back to default credentials")
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			path, err := store.path()
			if err != nil || filepath.Dir(path) != custom || !store.exists() || store.load() != nil {
				t.Fatalf("YAML save/load/existence failed: %q, %v", path, err)
			}
			if err := os.WriteFile(path, []byte("invalid-json"), 0o600); err != nil {
				t.Fatal(err)
			}
			if store.load() == nil {
				t.Fatal("corrupt YAML store fell back to default credentials")
			}
			if err := store.clear(); err != nil || store.exists() {
				t.Fatalf("YAML clear failed: %v", err)
			}
			unchanged, err := os.ReadFile(defaultPath)
			if err != nil || string(unchanged) != string(original) {
				t.Fatalf("default credentials changed: %v", err)
			}
		})
	}
}

func assertUnchangedCredentialDirectory(t *testing.T, dir, filename string, before []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil || string(got) != string(before) {
		t.Fatalf("refresh touched newly selected store: %v", err)
	}
	for _, suffix := range []string{".lock", ".refresh.lock"} {
		if _, err := os.Stat(filepath.Join(dir, filename+suffix)); !os.IsNotExist(err) {
			t.Fatalf("refresh touched newly selected lock %s: %v", suffix, err)
		}
	}
}

func TestChatGPTRefreshPinsYAMLPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	first, second := t.TempDir(), t.TempDir()
	selectYAMLCredentialDir(t, first)
	creds := &ChatGPTCredentials{AccessToken: "first", RefreshToken: "first-refresh", ExpiresAt: time.Now().Add(-time.Hour).Unix(), AccountID: "first-account"}
	if err := SaveChatGPTCredentials(creds); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(second, "chatgpt_oauth.json")
	if err := saveChatGPTCredentials(secondPath, &ChatGPTCredentials{AccessToken: "second", RefreshToken: "second-refresh", AccountID: "second-account"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	oldRefresh := refreshChatGPTToken
	t.Cleanup(func() { refreshChatGPTToken = oldRefresh })
	refreshChatGPTToken = func(token string) (*oauth.ChatGPTTokenResponse, error) {
		if token != "first-refresh" {
			t.Fatalf("wrong generation exchanged: %q", token)
		}
		selectYAMLCredentialDir(t, second)
		return &oauth.ChatGPTTokenResponse{AccessToken: "rotated", RefreshToken: "rotated-refresh", ExpiresIn: 3600}, nil
	}
	if err := RefreshChatGPTCredentials(creds); err != nil {
		t.Fatal(err)
	}
	stored, err := readChatGPTCredentials(filepath.Join(first, "chatgpt_oauth.json"))
	if err != nil || stored.AccessToken != "rotated" || stored.AccountID != "first-account" {
		t.Fatalf("original store not refreshed: %+v, %v", stored, err)
	}
	assertUnchangedCredentialDirectory(t, second, "chatgpt_oauth.json", before)
	selected, err := GetChatGPTCredentials()
	if err != nil || selected.AccountID != "second-account" {
		t.Fatalf("next operation did not select edited YAML: %+v, %v", selected, err)
	}
}

type pathEditingGrokClient struct {
	edit func()
}

func (c pathEditingGrokClient) RefreshToken(context.Context, string) (*oauth.GrokTokenResponse, error) {
	c.edit()
	return &oauth.GrokTokenResponse{AccessToken: "rotated", RefreshToken: "rotated-refresh", ExpiresIn: 3600}, nil
}

func (c pathEditingGrokClient) UserInfo(context.Context, string) (string, error) {
	return "first-account", nil
}

func TestGrokRefreshPinsYAMLPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	first, second := t.TempDir(), t.TempDir()
	selectYAMLCredentialDir(t, first)
	creds := &GrokCredentials{AccessToken: "first", RefreshToken: "first-refresh", ExpiresAt: time.Now().Add(-time.Hour).Unix(), AccountID: "first-account"}
	if err := SaveGrokCredentials(creds); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(second, grokCredentialsFile)
	if err := saveGrokCredentials(secondPath, &GrokCredentials{AccessToken: "second", RefreshToken: "second-refresh", ExpiresAt: 1, AccountID: "second-account"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	client := pathEditingGrokClient{edit: func() { selectYAMLCredentialDir(t, second) }}
	if err := RefreshGrokCredentialsWithClient(context.Background(), creds, false, client); err != nil {
		t.Fatal(err)
	}
	stored, err := readGrokCredentials(filepath.Join(first, grokCredentialsFile))
	if err != nil || stored.AccessToken != "rotated" || stored.AccountID != "first-account" {
		t.Fatalf("original store not refreshed: %+v, %v", stored, err)
	}
	assertUnchangedCredentialDirectory(t, second, grokCredentialsFile, before)
}
