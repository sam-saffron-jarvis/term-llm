package clibin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

const claudeProxyTestToken = "sk-tlp-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func claudeProxyTestCIDR(t *testing.T, raw string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

func claudeProxyTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "term-llm Claude proxy test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:                 []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"api.anthropic.com", "platform.claude.com"},
		ExcludedDNSDomains:          []string{".api.anthropic.com", ".platform.claude.com"},
		ExcludedIPRanges:            []*net.IPNet{claudeProxyTestCIDR(t, "0.0.0.0/0"), claudeProxyTestCIDR(t, "::/0")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func claudeProxyMetadataServer(t *testing.T, caPEM []byte, route string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/mounted/claude-bin/.well-known/term-llm/claude-proxy" || r.URL.RawQuery != "" {
			t.Errorf("unexpected Claude bootstrap request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+claudeProxyTestToken || r.Header.Get("x-api-key") != "" {
			t.Errorf("unexpected Claude bootstrap credential")
		}
		if r.Header.Get("X-Term-LLM-Proxy-App") != "term-llm" {
			t.Errorf("missing Claude bootstrap attribution")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"protocol_version": 2, "token_mode": "vault",
			"hosts":           []string{"api.anthropic.com", "platform.claude.com"},
			"certificate_pem": string(caPEM), "proxy_url": "http://" + r.Host, "route": route,
		})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestClaudeProxyEnvironmentUsesNativeTunnelForAllLaunches(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "real-inherited-canary")
	t.Setenv("ANTHROPIC_API_KEY", "real-api-canary")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "real-oauth-canary")
	t.Setenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "real-refresh-canary")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "9")
	t.Setenv("CLAUDE_CODE_SESSIONS_OAUTH_TOKEN", "real-session-canary")
	t.Setenv("ANTHROPIC_UNIX_SOCKET", "/real/anthropic.sock")
	t.Setenv("CLAUDE_CODE_CUSTOM_OAUTH_URL", "https://real-oauth.example.test")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "x-api-key: real-header-canary")
	t.Setenv("CLAUDE_CONFIG_DIR", "/not-the-worker-config")
	t.Setenv("ANTHROPIC_BASE_URL", "https://not-the-proxy.example.test")
	t.Setenv("HTTPS_PROXY", "http://ambient.example.test")
	t.Setenv("https_proxy", "http://ambient.example.test")
	t.Setenv("NO_PROXY", "api.anthropic.com")
	caPEM := claudeProxyTestCA(t)
	server, calls := claudeProxyMetadataServer(t, caPEM, "claude-bin")
	root := server.URL + "/mounted/claude-bin"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: "claude-bin", Token: claudeProxyTestToken}}); err != nil {
		t.Fatal(err)
	}
	provider := NewClaudeBinProvider("opus-high", map[string]string{claudeProxyRootEnv: root})
	expectedState, err := proxyclient.ClaudeNativeStateDir(root, claudeProxyTestToken)
	if err != nil {
		t.Fatal(err)
	}
	dir := ""
	for _, env := range [][]string{provider.buildCommandEnv("high"), provider.buildModelListEnv()} {
		cmd := &exec.Cmd{Env: env}
		cleanup, err := prepareClaudeLaunch(t.Context(), "claude-bin-models", cmd)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		values := envSliceMap(cmd.Env)
		if _, ok := values["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
			t.Fatal("launcher must not inject a vendor OAuth token")
		}
		if _, ok := values["CLAUDE_CODE_OAUTH_SCOPES"]; ok {
			t.Fatal("launcher must not inject OAuth scopes")
		}
		if values["ANTHROPIC_BASE_URL"] != "" || values["ANTHROPIC_AUTH_TOKEN"] != "" || values["ANTHROPIC_API_KEY"] != "" || values["CLAUDE_CODE_API_BASE_URL"] != "" || values["ANTHROPIC_UNIX_SOCKET"] != "" {
			t.Fatal("upstream auth/root/socket inherited into proxy child")
		}
		if values["ANTHROPIC_CUSTOM_HEADERS"] != "X-Term-LLM-Proxy-App: term-llm" {
			t.Fatal("attribution header missing")
		}
		for _, key := range []string{"CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_SESSIONS_OAUTH_TOKEN", "ANTHROPIC_PROFILE", "ALL_PROXY", "all_proxy", "http_proxy"} {
			if _, ok := values[key]; ok {
				t.Errorf("competing native credential %s leaked", key)
			}
		}
		if values["NO_PROXY"] != values["no_proxy"] || !strings.Contains(values["NO_PROXY"], "localhost") || !strings.Contains(values["NO_PROXY"], "127.0.0.1") || !strings.Contains(values["NO_PROXY"], "::1") {
			t.Fatal("native API exclusion list does not keep loopback outside the tunnel")
		}
		if strings.Contains(strings.ToLower(values["NO_PROXY"]), "api.anthropic.com") || strings.Contains(values["NO_PROXY"], "*") {
			t.Fatal("native API could bypass the tunnel")
		}
		if values["CLAUDE_CODE_DISABLE_WORKFLOWS"] != "1" || values["ENABLE_TOOL_SEARCH"] != "false" {
			t.Fatal("lost tool isolation")
		}
		if values["CLAUDE_CONFIG_DIR"] != expectedState || values["CLAUDE_CONFIG_DIR"] == "/not-the-worker-config" {
			t.Fatalf("not a private worker config: %q", values["CLAUDE_CONFIG_DIR"])
		}
		if values["ANTHROPIC_CONFIG_DIR"] != expectedState {
			t.Fatalf("not a private worker profile config: %q", values["ANTHROPIC_CONFIG_DIR"])
		}
		if dir != "" && dir != values["CLAUDE_CONFIG_DIR"] {
			t.Fatal("model and inference lost shared native session state")
		}
		dir = values["CLAUDE_CONFIG_DIR"]
		if _, ok := values[claudeProxyRootEnv]; ok {
			t.Fatal("internal marker leaked to CLI")
		}
		if values["HTTPS_PROXY"] != values["HTTP_PROXY"] || values["HTTPS_PROXY"] == "" {
			t.Fatal("native HTTPS proxy variables differ")
		}
		proxy, err := url.Parse(values["HTTPS_PROXY"])
		if err != nil || proxy.User == nil || proxy.User.Username() != "claude-bin" || proxy.User.String() == "" {
			t.Fatalf("native proxy identity missing: %v", err)
		}
		password, _ := proxy.User.Password()
		if password != claudeProxyTestToken || proxy.Path != "" || proxy.RawQuery != "" || proxy.Fragment != "" {
			t.Fatal("native proxy URL is not the scoped origin")
		}
		caPath := values["NODE_EXTRA_CA_CERTS"]
		if filepath.Dir(caPath) != dir {
			t.Fatalf("CA is not isolated with native state: %q", caPath)
		}
		info, err := os.Stat(caPath)
		if err != nil {
			t.Fatalf("CA stat: %v", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("CA mode = %v", info.Mode().Perm())
		}
		data, err := os.ReadFile(caPath)
		if err != nil || !strings.Contains(string(data), "BEGIN CERTIFICATE") || strings.Contains(string(data), "PRIVATE KEY") {
			t.Fatalf("CA content is not public-only: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("metadata bootstrap calls = %d, want 2", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("proxy preparation manufactured onboarding completion state")
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"forceLoginMethod": "claudeai"`) {
		t.Fatalf("forced claude.ai login setting missing: %q", data)
	}
	if strings.Contains(string(data), claudeProxyTestToken) || strings.Contains(string(data), "canary") {
		t.Fatal("created vendor credential file")
	}
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatal("gateway mode created OAuth login")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("native state mode = %v, err=%v", info, err)
		}
	}
}

func TestClaudeProxyMissingBindingFailsBeforeExec(t *testing.T) {
	_, err := prepareClaudeLaunch(t.Context(), "claude-bin", &exec.Cmd{Env: []string{claudeProxyRootEnv + "=https://missing-claude-proxy.example.test/root"}})
	if err == nil || strings.Contains(err.Error(), "missing-claude-proxy") {
		t.Fatalf("unregistered root accepted or leaked: %v", err)
	}
}

func TestClaudeProxyBootstrapFailureDoesNotFallBackToNativeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell CLI fixture")
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "native-executed")
	script := "#!/bin/sh\nprintf ran > " + marker + "\nexit 99\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "secret-body "+claudeProxyTestToken)
	}))
	defer server.Close()
	root := server.URL + "/mounted/claude-bin"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: "claude-bin", Token: claudeProxyTestToken}}); err != nil {
		t.Fatal(err)
	}
	provider := NewClaudeBinProvider("opus", map[string]string{claudeProxyRootEnv: root})
	_, _, _, _, err := provider.prepareClaudeCommand(t.Context(), nil, "", "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("bootstrap failure was not surfaced: %v", err)
	}
	for _, secret := range []string{claudeProxyTestToken, "secret-body", "/mounted/claude-bin"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("bootstrap error leaked %q: %v", secret, err)
		}
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("proxy bootstrap failure executed the local Claude CLI")
	}
}

func TestClaudeProxyLeavesUnboundLaunchUntouched(t *testing.T) {
	env := []string{"HOME=/home/user", "ANTHROPIC_AUTH_TOKEN=direct-oauth", "PRESERVED=value"}
	cmd := &exec.Cmd{Env: env}
	cleanup, err := prepareClaudeLaunch(t.Context(), "claude-bin", cmd)
	if err != nil {
		t.Fatalf("direct launch changed: %v", err)
	}
	defer cleanup()
	if strings.Join(cmd.Env, "\n") != strings.Join(env, "\n") {
		t.Fatalf("unbound environment changed: %v", cmd.Env)
	}
}

// The proxy route marker is stripped while preparing the launch, so readings
// must follow the route the launcher validated rather than the final child env.
func TestClaudeRateLimitsFollowValidatedProxyLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell CLI fixture")
	}
	resetObservedClaudeRateLimits(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "token-a")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	server, _ := claudeProxyMetadataServer(t, claudeProxyTestCA(t), "claude-bin")
	root := server.URL + "/mounted/claude-bin"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: "claude-bin", Token: claudeProxyTestToken}}); err != nil {
		t.Fatal(err)
	}
	provider := NewClaudeBinProvider("opus", map[string]string{claudeProxyRootEnv: root})
	cmd, stdin, owned, account, err := provider.prepareClaudeCommand(t.Context(), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	defer owned.Close()
	if envValue(cmd.Env, claudeProxyRootEnv) != "" || account == "" || account != proxyClaudeAccount(root) {
		t.Fatalf("account = %q", account)
	}

	recordClaudeRateLimits(withClaudeAccount(t.Context(), account), claudeRateLimitEventLine)
	if got, ok := ObservedClaudeRateLimits(root, nil); !ok || got.Windows["five_hour"].Utilization != 0.15 {
		t.Fatalf("proxy reading = %+v ok=%v", got, ok)
	}
	if got, ok := ObservedClaudeRateLimits("", []string{"CLAUDE_CODE_OAUTH_TOKEN=token-a"}); ok {
		t.Fatalf("proxy reading attributed to the direct login: %+v", got)
	}
}
