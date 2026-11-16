package clibin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestClaudeProxyEnvironmentForcesGatewayAndPrivateConfigForAllLaunches(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "real-inherited-canary")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "real-oauth-canary")
	t.Setenv("CLAUDE_CONFIG_DIR", "/not-the-worker-config")
	t.Setenv("ANTHROPIC_BASE_URL", "https://not-the-proxy.example.test")
	root := "https://claude-proxy-env.example.test/claude-code"
	token := strings.Repeat("s", 40)
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: "claude-code", Token: token}}); err != nil {
		t.Fatal(err)
	}
	provider := NewClaudeBinProvider("opus-high", map[string]string{claudeProxyRootEnv: root})
	dir := ""
	for _, env := range [][]string{provider.buildCommandEnv("high"), provider.buildModelListEnv()} {
		final, server, err := startCLIWireAudit("claude-bin-models", env)
		if err != nil {
			t.Fatal(err)
		}
		if server != nil {
			t.Fatal("unexpected wire capture")
		}
		values := envSliceMap(final)
		if values["ANTHROPIC_AUTH_TOKEN"] != token || values["ANTHROPIC_BASE_URL"] != root || values["CLAUDE_CODE_OAUTH_TOKEN"] != "" || values["ANTHROPIC_API_KEY"] != "" {
			t.Fatal("upstream auth/root inherited into proxy child")
		}
		if values["CLAUDE_CODE_DISABLE_WORKFLOWS"] != "1" || values["ENABLE_TOOL_SEARCH"] != "false" {
			t.Fatal("lost tool isolation")
		}
		if !strings.HasPrefix(values["CLAUDE_CONFIG_DIR"], cache+string(os.PathSeparator)) || values["CLAUDE_CONFIG_DIR"] == "/not-the-worker-config" {
			t.Fatal("not a private worker config")
		}
		if dir != "" && dir != values["CLAUDE_CONFIG_DIR"] {
			t.Fatal("model and inference lost shared native session state")
		}
		dir = values["CLAUDE_CONFIG_DIR"]
		if _, ok := values[claudeProxyRootEnv]; ok {
			t.Fatal("internal marker leaked to CLI")
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) || strings.Contains(string(data), "canary") {
		t.Fatal("created vendor credential file")
	}
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("gateway mode created OAuth login")
	}
}
func TestClaudeProxyMissingBindingFailsBeforeExec(t *testing.T) {
	_, _, err := startCLIWireAudit("claude-bin", []string{claudeProxyRootEnv + "=https://missing-claude-proxy.example.test/root"})
	if err == nil {
		t.Fatal("unregistered root accepted")
	}
}
