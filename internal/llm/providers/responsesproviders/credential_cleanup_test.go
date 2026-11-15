package responsesproviders

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/credentials"
	"github.com/term-llm/term-llm/internal/oauth"
)

func cleanupPathFixture(t *testing.T, filename string) (string, string, func()) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", "")
	first, second := t.TempDir(), t.TempDir()
	payload := []byte(`{"access_token":"expired","refresh_token":"same-refresh","expires_at":1,"account_id":"acct_1"}`)
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, filename), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	selectDir := func(dir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(fmt.Sprintf("credentials_dir: %q\n", dir)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	selectDir(first)
	t.Cleanup(func() {
		got, err := os.ReadFile(filepath.Join(second, filename))
		if err != nil || string(got) != string(payload) {
			t.Errorf("cleanup touched newly selected credentials: %q, %v", got, err)
		}
		for _, suffix := range []string{".lock", ".refresh.lock"} {
			if _, err := os.Stat(filepath.Join(second, filename+suffix)); !os.IsNotExist(err) {
				t.Errorf("cleanup touched newly selected lock %s: %v", suffix, err)
			}
		}
		if _, err := os.Stat(filepath.Join(first, filename)); !os.IsNotExist(err) {
			t.Errorf("rejected original credentials were not cleared: %v", err)
		}
	})
	return first, second, func() { selectDir(second) }
}

func TestChatGPTInvalidRefreshCleanupPinsYAMLPath(t *testing.T) {
	_, _, edit := cleanupPathFixture(t, "chatgpt_oauth.json")
	creds, err := credentials.GetChatGPTCredentials()
	if err != nil {
		t.Fatal(err)
	}
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != oauth.ChatGPTTokenEndpoint {
			return nil, fmt.Errorf("unexpected fixture request: %s", req.URL)
		}
		edit()
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
	})
	err = NewChatGPTResponsesClient(creds).OnAuthRetry(context.Background())
	if err == nil || !strings.Contains(err.Error(), "re-authenticate") {
		t.Fatalf("invalid refresh guidance = %v", err)
	}
}

type switchingInvalidGrokClient struct {
	*fakeGrokOAuthAPI
	edit func()
}

func (c switchingInvalidGrokClient) RefreshToken(context.Context, string) (*oauth.GrokTokenResponse, error) {
	c.edit()
	return nil, oauth.ErrGrokRefreshTokenInvalid
}

func TestGrokInvalidRefreshCleanupPinsYAMLPath(t *testing.T) {
	_, _, edit := cleanupPathFixture(t, "grok_oauth.json")
	creds, err := credentials.GetGrokCredentials()
	if err != nil {
		t.Fatal(err)
	}
	oldClient := grokOAuthClient
	t.Cleanup(func() { grokOAuthClient = oldClient })
	grokOAuthClient = switchingInvalidGrokClient{fakeGrokOAuthAPI: &fakeGrokOAuthAPI{}, edit: edit}
	err = newGrokResponsesClient(creds).OnAuthRetry(context.Background())
	if !errors.Is(err, oauth.ErrGrokRefreshTokenInvalid) || !strings.Contains(err.Error(), "auth login grok") {
		t.Fatalf("invalid refresh guidance = %v", err)
	}
}
