package doctor

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestProxyConfigFindingsDoNotResolveUnusedVendorSecrets(t *testing.T) {
	cfg := &config.Config{DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{
		"openai":     {ProxyBinding: "https://proxy.example.test/openai", APIKey: "$(must-not-execute-vendor-command)"},
		"cursor-bin": {ProxyBinding: "gated:CLI protocol unsupported", APIKey: "$(must-not-execute-either)"},
	}}
	check := &ConfigCheck{Config: cfg, ResolveDeferred: true}
	findings := check.providerFindings(map[string]bool{"openai": true, "cursor-bin": true})
	var proxy, gate bool
	for _, f := range findings {
		if strings.Contains(f.Title, "no usable credential") || strings.Contains(f.Detail, "must-not-execute") {
			t.Fatalf("looked up unused local credentials: %+v", f)
		}
		if strings.Contains(f.Title, `provider "openai" uses the trusted proxy`) {
			proxy = true
			if !strings.Contains(f.Detail, "not checked offline") {
				t.Fatal("offline health claim")
			}
		}
		if strings.Contains(f.Title, "blocked in proxy mode") {
			gate = true
			if f.Severity != SeverityWarn || f.Remedy == "" {
				t.Fatal("unactionable gate")
			}
		}
	}
	if !proxy || !gate {
		t.Fatalf("missing proxy/gate findings: %+v", findings)
	}
}
