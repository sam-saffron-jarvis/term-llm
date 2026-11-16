package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

// Construction must never prompt, exchange OAuth, read a poisoned credential
// store or execute an old vendor key command. This is not a wire/vendor test.
func TestEveryProxyProviderConstructsOrNamesItsExplicitGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	credentials := filepath.Join(home, "credentials")
	if err := os.MkdirAll(credentials, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM_LLM_CREDENTIALS_DIR", credentials)
	for _, name := range []string{"chatgpt_oauth.json", "grok_oauth.json", "copilot.json"} {
		if err := os.WriteFile(filepath.Join(credentials, name), []byte("POISON: local credentials must never be consulted"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	providers := map[string]any{}
	for _, spec := range config.BuiltinProviders() {
		providers[spec.Name] = map[string]any{"api_key": "$(exit 99)", "headers": map[string]string{"Authorization": "$(exit 98)"}}
	}
	data, err := json.Marshal(map[string]any{"provider_discovery": "config", "proxy": map[string]any{"url": "https://factory-proxy-only.example.test", "token": strings.Repeat("p", 40)}, "providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "term-llm", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range config.BuiltinProviders() {
		t.Run(spec.Name, func(t *testing.T) {
			profile, ok := proxyprofile.ForProvider(spec.Type)
			if !ok {
				t.Fatal("missing explicit proxy decision")
			}
			provider, err := NewProviderByNameNoRetry(cfg, spec.Name, "")
			if profile.Gate != "" {
				if err == nil || !strings.Contains(err.Error(), "credentiallessly through the proxy") {
					t.Fatalf("missing named fail-closed gate: %v", err)
				}
				return
			}
			if err != nil || provider == nil {
				t.Fatalf("credentialless construction failed: %v", err)
			}
		})
	}
}
