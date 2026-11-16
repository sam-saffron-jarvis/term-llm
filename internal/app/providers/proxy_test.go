package providers

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestProxyDiagnosticsNeverProbeLocalLoginOrPrintScopedToken(t *testing.T) {
	token := "synthetic-private-scoped-token"
	cfg := &config.Config{ProviderDiscovery: config.ProviderDiscoveryConfig, Proxy: config.ProxyConfig{URL: "https://proxy.example.test", Token: token}, Providers: map[string]config.ProviderConfig{
		"openai":     {BaseURL: "https://proxy.example.test/openai", ProxyBinding: "https://proxy.example.test/openai", APIKey: token},
		"cursor-bin": {ProxyBinding: "gated:synthetic unsupported CLI contract"},
		"gemini":     {APIKey: "local-direct-token"},
	}}
	list := BuildListWith(cfg, func(string) bool { t.Fatal("proxy diagnostics consulted local login"); return false })
	byName := map[string]Info{}
	for _, p := range list {
		byName[p.Name] = p
	}
	if p := byName["openai"]; p.Routing != "proxy_configured" || p.ProxyRoute != "https://proxy.example.test/openai" || p.RequiresKey || p.EnvVar != "" {
		t.Fatalf("wrong delegated status: %+v", p)
	}
	if p := byName["cursor-bin"]; p.Routing != "proxy_blocked" || !strings.Contains(p.ProxyGate, "unsupported") {
		t.Fatalf("gate hidden: %+v", p)
	}
	if byName["gemini"].Routing != "direct_exception" {
		t.Fatal("direct custody exception hidden")
	}
	data, err := json.Marshal(list)
	if err != nil || bytes.Contains(data, []byte(token)) || bytes.Contains(data, []byte("local-direct-token")) {
		t.Fatal("credential in provider metadata", err)
	}
	var out bytes.Buffer
	if err := showProviderDetails(Options{Stdout: &out}, "openai", list); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "grants/connectivity not checked") || strings.Contains(out.String(), token) {
		t.Fatal("misleading/secret-bearing detail", out.String())
	}
}
