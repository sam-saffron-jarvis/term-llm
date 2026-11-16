package clibin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/acp"
	"github.com/term-llm/term-llm/internal/providerspec"
	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

const cliProxyTestToken = "scoped-cli-proxy-token-0123456789abcdef"

func registerCLIProxyBindings(t *testing.T, kind string, roots ...string) {
	t.Helper()
	bindings := make([]proxyclient.Binding, 0, len(roots))
	for _, root := range roots {
		bindings = append(bindings, proxyclient.Binding{Root: root, Kind: kind, Token: cliProxyTestToken})
	}
	if err := proxyclient.Register(bindings); err != nil {
		t.Fatal(err)
	}
}

func assertNoCanary(t *testing.T, env []string) {
	t.Helper()
	for _, entry := range env {
		if strings.Contains(entry, "canary") {
			t.Fatalf("inherited or configured value reached the proxy child: %s", entry)
		}
	}
}

func TestGrokProxyEnvironmentForcesScopedRootsAndPrivateAuth(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "real-home-canary"))
	t.Setenv("XAI_API_KEY", "real-api-key-canary")
	t.Setenv("GROK_AUTH", "real-inline-auth-canary")
	t.Setenv("GROK_CONFIG", "/overlay-canary.toml")
	t.Setenv("GROK_AUTH_PROVIDER_COMMAND", "print-token-canary")
	t.Setenv("GROK_MODELS_BASE_URL", "https://direct-canary.example.test/v1")
	root := "https://grok-proxy-env.example.test/grok-bin"
	registerCLIProxyBindings(t, "grok", root)
	provider := NewGrokBinProvider("grok-4.5", map[string]string{
		grokProxyRootEnv: root, "GROK_AUTH_PATH": "/configured-canary/auth.json", "GROK_WS_URL": "wss://canary.example.test",
	})
	provider.SetPreferOAuth(true)
	for _, home := range []string{t.TempDir(), t.TempDir()} {
		final, server, err := startCLIWireAudit("grok-bin-models", provider.buildCommandEnvForHome(home))
		if err != nil || server != nil {
			t.Fatalf("prepare: %v", err)
		}
		assertNoCanary(t, final)
		values := envSliceMap(final)
		for key, want := range map[string]string{
			"XAI_API_KEY": cliProxyTestToken, "GROK_HOME": home, "GROK_MODELS_BASE_URL": root, "GROK_XAI_API_BASE_URL": root,
			"GROK_CLI_CHAT_PROXY_BASE_URL": root, "GROK_MODELS_LIST_URL": root + "/models", "GROK_DISABLE_AUTOUPDATER": "1",
		} {
			if values[key] != want {
				t.Errorf("%s=%q, want %q", key, values[key], want)
			}
		}
		for _, key := range []string{"GROK_AUTH_PATH", "HOME", "XDG_CONFIG_HOME"} {
			if !strings.HasPrefix(values[key], filepath.Join(cache, "term-llm", "grok-bin-proxy")+string(os.PathSeparator)) {
				t.Errorf("%s=%q is not in the private proxy namespace", key, values[key])
			}
		}
		if _, err := os.Stat(values["GROK_AUTH_PATH"]); !os.IsNotExist(err) {
			t.Error("proxy mode created a Grok login")
		}
		if _, ok := values[grokProxyRootEnv]; ok {
			t.Error("internal marker leaked to CLI")
		}
		// A home that never had a config written (model listing) is pinned too.
		config, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"preferred_method = \"api_key\"", "telemetry = false", "models_base_url = \"" + root + "\""} {
			if !strings.Contains(string(config), want) {
				t.Errorf("config missing %q", want)
			}
		}
		if strings.Contains(string(config), cliProxyTestToken) {
			t.Error("scoped token persisted in config")
		}
	}
}

func TestGrokProxyConfigSurvivesOwnedConfigRewrites(t *testing.T) {
	root := "https://grok-proxy-config.example.test/grok-bin"
	registerCLIProxyBindings(t, "grok", root)
	provider := NewGrokBinProvider("grok-4.5", map[string]string{grokProxyRootEnv: root})
	provider.grokHome = t.TempDir()
	if err := provider.writeConfig("http://127.0.0.1:1/mcp", "mcp-token"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(provider.grokHome, "config.toml")
	written, _ := os.ReadFile(path)
	if !strings.Contains(string(written), "[mcp_servers.term-llm]") || strings.Count(string(written), grokProxyConfigMarker) != 1 {
		t.Fatalf("MCP bridge or routing fragment missing:\n%s", written)
	}
	// A stale fragment for another root is replaced, not accumulated.
	if err := ensureGrokProxyConfig(provider.grokHome, root+"-other"); err != nil {
		t.Fatal(err)
	}
	if err := ensureGrokProxyConfig(provider.grokHome, root); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(written) {
		t.Fatalf("config not restored:\n%s", again)
	}
	if direct := NewGrokBinProvider("grok-4.5", nil); direct.grokProxyConfig() != "" {
		t.Fatal("direct provider received proxy routing")
	}
}

func TestGrokProxyNeverSelectsCachedSession(t *testing.T) {
	both := []acp.AuthMethod{{ID: "cached_token"}, {ID: grokACPAPIKeyMethod}}
	proxied := NewGrokBinProvider("grok-4.5", map[string]string{grokProxyRootEnv: "https://grok-proxy-auth.example.test/grok-bin"})
	proxied.SetPreferOAuth(true)
	if got := proxied.selectGrokACPAuthMethod(both); got != grokACPAPIKeyMethod {
		t.Fatalf("proxy auth method = %q", got)
	}
	if got := proxied.selectGrokACPAuthMethod(both[:1]); got != "" {
		t.Fatalf("proxy mode fell back to %q", got)
	}
	direct := NewGrokBinProvider("grok-4.5", nil)
	direct.SetPreferOAuth(true)
	if got := direct.selectGrokACPAuthMethod(both); got != "cached_token" {
		t.Fatalf("direct auth method changed: %q", got)
	}
}

func TestCursorProxyForcesAllRootsTokenAndPrivateCredentialNamespace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cursor-bin proxy mode fails closed off Linux")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_CONFIG_HOME", "/real-xdg-canary")
	t.Setenv("CURSOR_CONFIG_DIR", "/real-config-canary")
	t.Setenv("CURSOR_API_KEY", "real-api-key-canary")
	t.Setenv("CURSOR_API_ENDPOINT", "https://direct-canary.example.test")
	root := "https://cursor-proxy-env.example.test/cursor-bin"
	registerCLIProxyBindings(t, "cursor", root, root+"-agent", root+"-repo")
	provider := NewCursorBinProvider("auto-smart", map[string]string{
		cursorProxyRootEnv: root, "CURSOR_AUTH_TOKEN": "configured-token-canary", "AGENT_CLI_CREDENTIAL_STORE": "file",
	})
	if provider.cursorCredentialStore() != "memory" {
		t.Fatal("proxy mode would import the real Cursor login")
	}
	home := t.TempDir()
	for _, launch := range []string{"cursor-bin", "cursor-bin-models"} {
		final, server, err := startCLIWireAudit(launch, provider.buildCommandEnv(home))
		if err != nil || server != nil {
			t.Fatalf("prepare: %v", err)
		}
		assertNoCanary(t, final)
		values := envSliceMap(final)
		private := filepath.Join(cache, "term-llm", "cursor-bin-proxy") + string(os.PathSeparator)
		if values["CURSOR_AUTH_TOKEN"] != cliProxyTestToken || values["CURSOR_API_ENDPOINT"] != root || values["CURSOR_API_BASE_URL"] != root {
			t.Fatal("scoped token or backend root not forced")
		}
		if !strings.HasPrefix(values["CURSOR_CONFIG_DIR"], private) || !strings.HasPrefix(values["XDG_CONFIG_HOME"], private) {
			t.Fatal("config or credential file root is not private")
		}
		if values["HOME"] != filepath.Join(home, cursorUserHomeDir) || values["CURSOR_DATA_DIR"] != filepath.Join(home, "data") {
			t.Fatal("lost owned home and session isolation")
		}
		// cursor-agent's hardcoded vendor endpoint must land on the dead sink.
		if values["NODE_USE_ENV_PROXY"] != "1" || values["HTTPS_PROXY"] != cursorProxyEgressSink || values["https_proxy"] != cursorProxyEgressSink ||
			values["NO_PROXY"] != "127.0.0.1,localhost,::1,cursor-proxy-env.example.test" || values["no_proxy"] != values["NO_PROXY"] {
			t.Fatalf("egress sink not forced: %q %q", values["HTTPS_PROXY"], values["NO_PROXY"])
		}
		if _, ok := values["CURSOR_API_KEY"]; ok {
			t.Fatal("API key reached proxy child; it would trigger a vendor token exchange")
		}
		var config struct {
			Network struct {
				UseHTTP1ForAgent bool `json:"useHttp1ForAgent"`
			} `json:"network"`
		}
		data, err := os.ReadFile(filepath.Join(values["CURSOR_CONFIG_DIR"], "cli-config.json"))
		if err != nil || json.Unmarshal(data, &config) != nil || !config.Network.UseHTTP1ForAgent {
			t.Fatalf("HTTP/1 agent transport not selected: %s %v", data, err)
		}
		if strings.Contains(string(data), cliProxyTestToken) {
			t.Fatal("scoped token persisted in config")
		}
	}
	for _, original := range [][]string{{"models"}, {"--endpoint", "https://evil.example.test", "--agent-endpoint=https://evil.example.test", "-k", "--print", "--model", "auto"}} {
		args := provider.cursorProxyArgs(original)
		joined := strings.Join(args, " ")
		want := "--endpoint " + root + " --agent-endpoint " + root + "-agent --repo-endpoint " + root + "-repo --http-version 1.1 --repo-http-version 1.1 "
		if !strings.HasPrefix(joined, want) || strings.Contains(joined, "evil") || strings.Contains(joined, "-k") || strings.Contains(joined, cliProxyTestToken) {
			t.Fatalf("args = %q", joined)
		}
		if args[len(args)-1] != original[len(original)-1] {
			t.Fatalf("lost original arguments: %q", joined)
		}
	}
	if got := provider.cursorDiagnosticRedactor()("denied " + cliProxyTestToken); strings.Contains(got, cliProxyTestToken) {
		t.Fatal("scoped token not redacted from diagnostics")
	}
	if direct := NewCursorBinProvider("auto-smart", nil); len(direct.cursorProxyArgs([]string{"models"})) != 1 {
		t.Fatal("direct provider received proxy arguments")
	}
}

func TestCursorProxyRequiresEveryRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cursor-bin proxy mode fails closed off Linux")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := "https://cursor-proxy-partial.example.test/cursor-bin"
	registerCLIProxyBindings(t, "cursor", root, root+"-agent")
	provider := NewCursorBinProvider("auto-smart", map[string]string{cursorProxyRootEnv: root})
	if _, _, err := startCLIWireAudit("cursor-bin", provider.buildCommandEnv(t.TempDir())); err == nil {
		t.Fatal("launch accepted without a scoped repository root")
	}
}

func TestVibeProxySelectsOnlyTheScopedGenericProvider(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("MISTRAL_API_KEY", "real-api-key-canary")
	t.Setenv("VIBE_CLI_OIDC_ENABLED", "canary")
	t.Setenv("VIBE_ACTIVE_PROVIDER", "canary")
	root := "https://vibe-proxy-env.example.test/vibe-bin"
	registerCLIProxyBindings(t, "", root)
	provider := NewVibeBinProvider("devstral-2", map[string]string{vibeProxyRootEnv: root, "MISTRAL_API_KEY": "configured-key-canary", "VIBE_MODELS": "canary"})
	final, server, err := startCLIWireAudit("vibe-bin", provider.buildVibeCommandEnv("devstral-2", "/owned-direct-home-canary"))
	if err != nil || server != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertNoCanary(t, final)
	values := envSliceMap(final)
	var providers, models []map[string]string
	if json.Unmarshal([]byte(values["VIBE_PROVIDERS"]), &providers) != nil || json.Unmarshal([]byte(values["VIBE_MODELS"]), &models) != nil || len(providers) != 1 || len(models) != 1 {
		t.Fatalf("providers=%q models=%q", values["VIBE_PROVIDERS"], values["VIBE_MODELS"])
	}
	if providers[0]["api_base"] != root || providers[0]["backend"] != "generic" || providers[0]["api_key_env_var"] != vibeProxyTokenEnv || providers[0]["name"] == "mistral" {
		t.Fatalf("provider = %v", providers[0])
	}
	if models[0]["name"] != "devstral-2" || models[0]["provider"] != providers[0]["name"] || values["VIBE_ACTIVE_MODEL"] != models[0]["alias"] {
		t.Fatalf("model = %v active = %q", models[0], values["VIBE_ACTIVE_MODEL"])
	}
	if values["VIBE_ENABLE_TELEMETRY"] != "false" || provider.vibeSelectedModel("devstral-2") != values["VIBE_ACTIVE_MODEL"] || NewVibeBinProvider("m", nil).vibeSelectedModel("m") != "m" {
		t.Fatal("telemetry not disabled or selected-model check does not follow the scoped alias")
	}
	if values[vibeProxyTokenEnv] != cliProxyTestToken || strings.Contains(values["VIBE_PROVIDERS"], cliProxyTestToken) {
		t.Fatal("scoped token not confined to its dedicated variable")
	}
	if !strings.HasPrefix(values["VIBE_HOME"], filepath.Join(cache, "term-llm", "vibe-bin-proxy")+string(os.PathSeparator)) {
		t.Fatalf("VIBE_HOME=%q is not private", values["VIBE_HOME"])
	}
	for key, want := range vibeForcedEnv {
		if values[key] != want {
			t.Errorf("lost tool restriction %s", key)
		}
	}
	if _, _, err := startCLIWireAudit("vibe-bin", provider.buildVibeCommandEnv("", "/owned")); err == nil {
		t.Fatal("proxy launch accepted without an explicit model")
	}
}

func TestCLIProxyMarkerWithoutBindingFailsBeforeExec(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for provider, marker := range map[string]string{
		"cursor-bin": cursorProxyRootEnv, "cursor-bin-models": cursorProxyRootEnv, "grok-bin": grokProxyRootEnv,
		"grok-bin-models": grokProxyRootEnv, "vibe-bin": vibeProxyRootEnv, "agy-bin-models": agyProxyRootEnv,
	} {
		env := []string{marker + "=https://unregistered-cli-proxy.example.test/" + provider, "HOME=/owned", "GROK_HOME=/owned", "VIBE_ACTIVE_MODEL=m"}
		if _, _, err := startCLIWireAudit(provider, env); err == nil {
			t.Errorf("%s accepted an unregistered proxy root", provider)
		}
	}
	// A binding of another credential kind is not a binding for this CLI.
	root := "https://cli-proxy-wrong-kind.example.test/grok-bin"
	registerCLIProxyBindings(t, "chatgpt", root)
	if _, _, err := startCLIWireAudit("grok-bin", []string{grokProxyRootEnv + "=" + root, "GROK_HOME=/owned"}); err == nil {
		t.Error("grok-bin accepted a binding of another credential kind")
	}
	// Antigravity fails closed even with a registered root.
	agyRoot := "https://agy-proxy-env.example.test/agy-bin"
	registerCLIProxyBindings(t, "", agyRoot)
	agy := NewAgyBinProvider("gemini", map[string]string{agyProxyRootEnv: agyRoot})
	if err := rejectAgyProxyEnvironment(agy.buildCommandEnv()); err == nil || !strings.Contains(err.Error(), agyBinMinimumVersion) {
		t.Errorf("agy-bin proxy launch not refused: %v", err)
	}
}

func TestCLIProxyLeavesDirectLaunchesUntouched(t *testing.T) {
	env := []string{"HOME=/home/user", "XAI_API_KEY=direct", "CURSOR_API_KEY=direct", "MISTRAL_API_KEY=direct", "GROK_HOME=/owned"}
	for _, provider := range []string{"cursor-bin", "grok-bin", "vibe-bin", "agy-bin-models", "claude-bin"} {
		final, _, err := startCLIWireAudit(provider, env)
		if err != nil || strings.Join(final, "\n") != strings.Join(env, "\n") {
			t.Errorf("%s direct environment changed: %v %v", provider, final, err)
		}
	}
}

// Configuration learns each adapter's marker from the profile; a mismatch would
// leave a proxy-bound provider on its local vendor login.
func TestCLIProxyMarkersMatchProfiles(t *testing.T) {
	for kind, marker := range map[providerspec.Type]string{
		providerspec.TypeClaudeBin: claudeProxyRootEnv, providerspec.TypeCursorBin: cursorProxyRootEnv,
		providerspec.TypeGrokBin: grokProxyRootEnv, providerspec.TypeVibeBin: vibeProxyRootEnv, providerspec.TypeAgyBin: agyProxyRootEnv,
	} {
		if profile, ok := proxyprofile.ForProvider(kind); !ok || profile.CLIRootEnv != marker {
			t.Errorf("%s profile marker = %q, adapter reads %q", kind, profile.CLIRootEnv, marker)
		}
	}
}
