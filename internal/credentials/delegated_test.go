package credentials

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestEndpointDelegationNeverAdoptsOrOverwritesLocalCredential(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", dir)
	local := &ChatGPTCredentials{AccessToken: "local-upstream-canary", RefreshToken: "local-refresh-canary", ExpiresAt: time.Now().Add(time.Hour).Unix(), AccountID: "local-account"}
	if err := SaveChatGPTCredentials(local); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chatgpt_oauth.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := "https://delegated-chatgpt.example.test/chatgpt"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: "chatgpt", Token: strings.Repeat("s", 40)}}); err != nil {
		t.Fatal(err)
	}
	delegated, err := GetChatGPTCredentialsForEndpoint(root + "/responses")
	if err != nil {
		t.Fatal(err)
	}
	if !delegated.Delegated() || delegated.AccessToken == local.AccessToken || delegated.RefreshToken != "" || delegated.AccountID == local.AccountID {
		t.Fatal("local login adopted by proxy client")
	}
	delegated.ExpiresAt = 1
	if err := RefreshChatGPTCredentials(delegated); err != nil {
		t.Fatal(err)
	}
	if err := RefreshRejectedChatGPTCredentials(delegated, delegated.AccessToken); err != nil {
		t.Fatal(err)
	}
	if err := SaveChatGPTCredentials(delegated); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("delegated operation mutated local login")
	}
	direct, err := GetChatGPTCredentialsForEndpoint("https://direct-custom.example.test/codex")
	if err != nil || direct.AccessToken != local.AccessToken || direct.Delegated() {
		t.Fatal("custom direct root lost native authentication")
	}
}
func TestDelegatedGrokAndCopilotNeedNoCredentialDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no-credentials-created")
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", dir)
	root := "https://delegated-other.example.test/"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root + "grok", Kind: "grok", Token: "scoped-grok"}, {Root: root + "copilot", Kind: "copilot", Token: "scoped-copilot"}}); err != nil {
		t.Fatal(err)
	}
	grok, err := GetGrokCredentialsForEndpoint(root + "grok")
	if err != nil || !grok.Delegated() {
		t.Fatal("Grok required a login")
	}
	grok.ExpiresAt = 1
	if err := RefreshGrokCredentialsWithClient(context.Background(), grok, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := SaveGrokCredentials(grok); err != nil {
		t.Fatal(err)
	}
	copilot, err := GetCopilotCredentialsForEndpoint(root + "copilot/models")
	if err != nil || !copilot.Delegated() {
		t.Fatal("Copilot required a login")
	}
	if err := SaveCopilotCredentials(copilot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("delegated client created/touched credential directory")
	}
}

func TestDelegatedCacheIdentityIncludesCallerWithoutExposingToken(t *testing.T) {
	root := "https://proxy-principal-cache.example.test/chatgpt"
	first := delegatedIdentity(root, "first-private-client-token")
	if first != delegatedIdentity(root, "first-private-client-token") {
		t.Fatal("identity is not stable")
	}
	if first == delegatedIdentity(root, "second-private-client-token") {
		t.Fatal("persistent model cache could cross caller principals")
	}
	if first == delegatedIdentity(root+"-other", "first-private-client-token") {
		t.Fatal("endpoint boundary lost")
	}
	if strings.Contains(first, "private-client-token") {
		t.Fatal("scoped token exposed in cache identity")
	}
}
