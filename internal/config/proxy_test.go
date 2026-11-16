package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

func TestProxyMaterializationSkipsLocalSecretsAndUsesAllValidBindings(t *testing.T) {
	cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-materialize.example.test", Token: strings.Repeat("t", 40)}, Providers: map[string]ProviderConfig{"openai": {APIKey: "$(touch should-not-run)", Headers: map[string]string{"Authorization": "$(touch also-not-run)"}}, "grok": {Type: ProviderTypeGrok, APIKey: "$(false)"}}}
	if err := cfg.materializeProxy(&fileSettings{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range proxyprofile.All() {
		for _, b := range p.Bindings {
			if strings.HasPrefix(b.Root, "providers.") {
				continue
			}
			f, ok := proxyStringFieldValue(&cfg, b.Root)
			if !ok || !strings.HasPrefix(f, cfg.Proxy.URL+"/") {
				t.Errorf("unbound %s=%q", b.Root, f)
			}
		}
	}
	pc := cfg.Providers["openai"]
	if pc.APIKey != cfg.Proxy.Token || pc.ProxyBinding == "" || len(pc.Headers) > 0 {
		t.Fatal("did not replace unused local credentials")
	}
	if err := pc.ResolveForInference(); err != nil {
		t.Fatal(err)
	}
	grok := cfg.Providers["grok"]
	if err := ValidateProviderCompatibility("grok", &grok); err != nil {
		t.Fatal(err)
	}
	if b, ok := proxyclient.Lookup(cfg.Providers["chatgpt"].BaseURL + "/responses"); !ok || b.Kind != "chatgpt" {
		t.Fatal("no endpoint-scoped account binding")
	}
}
func proxyStringFieldValue(c *Config, path string) (string, bool) {
	f, ok := proxyStringField(reflect.ValueOf(c).Elem(), strings.Split(path, "."))
	if !ok {
		return "", false
	}
	return f.String(), true
}

func TestProxyExplicitRootsCannotSilentlyBypassCustody(t *testing.T) {
	cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-explicit.example.test", Token: strings.Repeat("t", 40), Direct: []string{"gemini"}}, Providers: map[string]ProviderConfig{"openai": {BaseURL: "https://direct.example.test/v1", APIKey: "local-key"}, "gemini": {APIKey: "gemini-local"}}}
	file := &fileSettings{values: map[string]any{"providers": map[string]any{"openai": map[string]any{"base_url": "https://direct.example.test/v1"}}}}
	if err := cfg.materializeProxy(file); err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["openai"].APIKey != cfg.Proxy.Token || cfg.Providers["openai"].BaseURL != cfg.Proxy.URL+"/openai" || cfg.Providers["openai"].ProxyBinding == "" {
		t.Fatal("explicit base URL silently bypassed proxy custody")
	}
	if cfg.Providers["gemini"].APIKey != "gemini-local" || cfg.Providers["gemini"].ProxyBinding != "" {
		t.Fatal("explicit proxy.direct exception lost local credentials")
	}
}
func TestProxyCustomProviderCannotShadowAccountRoute(t *testing.T) {
	cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-shadow.example.test", Token: strings.Repeat("t", 40)}, Providers: map[string]ProviderConfig{"gemini-live": {Type: ProviderTypeChatGPT}}}
	if err := cfg.materializeProxy(&fileSettings{}); err == nil {
		t.Fatal("account delegation was silently replaced by a static credential binding")
	}
}

func TestProxyURLAndMissingTokenReject(t *testing.T) {
	for _, u := range []string{"http://remote.test", "https://u:secret@remote.test", "https://remote.test/?secret=1", "https://remote.test/#fragment", "https://remote.test/../x"} {
		if _, err := proxyRoot(u); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:123", "http://localhost:123", "https://remote.test"} {
		if _, err := proxyRoot(u); err != nil {
			t.Fatal(err)
		}
	}
}
func TestProxyScopedTokenFileAndNoVendorCommandExecution(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("s", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "should-not-exist")
	cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-file.example.test", Token: "file://" + tokenFile}, Providers: map[string]ProviderConfig{"openai": {APIKey: "$(touch " + marker + ")"}}}
	if err := cfg.materializeProxy(&fileSettings{}); err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers["openai"]
	if err := p.ResolveCredentials("openai"); err != nil {
		t.Fatal(err)
	}
	if err := p.ResolveForInference(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("vendor credential command executed")
	}
}

func TestProxyCustomAndAncillaryRootsRequireExplicitDirectException(t *testing.T) {
	cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-custom-required.example.test", Token: strings.Repeat("r", 40)}, Providers: map[string]ProviderConfig{"household": {Type: ProviderTypeOpenAICompat, BaseURL: "https://custom.example.test/v1", APIKey: "$(must-not-run)"}}}
	cfg.Image.OpenAI.BaseURL = "https://api.openai.com/v1"
	if err := cfg.materializeProxy(nil); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["household"]; got.BaseURL != cfg.Proxy.URL+"/household" || got.APIKey != cfg.Proxy.Token || got.ProxyBinding == "" {
		t.Fatalf("custom root escaped custody: %+v", got)
	}
	if !strings.HasPrefix(cfg.Image.OpenAI.BaseURL, cfg.Proxy.URL+"/") {
		t.Fatal("ancillary root escaped custody")
	}
}

func TestProxyExactChatURLIsBlockedRatherThanSilentlyChangingOperation(t *testing.T) {
	for _, kind := range []ProviderType{ProviderTypeOpenAICompat, ProviderTypeVLLM} {
		t.Run(string(kind), func(t *testing.T) {
			cfg := Config{Proxy: ProxyConfig{URL: "https://proxy-exact-" + strings.ReplaceAll(string(kind), "_", "-") + ".example.test", Token: strings.Repeat("x", 40)}, Providers: map[string]ProviderConfig{"exact-service": {Type: kind, URL: "https://vendor.example.test/custom/generate", APIKey: "$(must-not-execute)"}}}
			if err := cfg.materializeProxy(nil); err != nil {
				t.Fatal(err)
			}
			pc := cfg.Providers["exact-service"]
			if err := pc.ResolveForInference(); err == nil || !strings.Contains(err.Error(), "exact url endpoints") {
				t.Fatalf("exact operation silently changed: %v", err)
			}
			if pc.APIKey != "" || pc.ResolvedAPIKey != "" {
				t.Fatal("gated exact endpoint retained local credentials")
			}
		})
	}
}

func TestProxyDirectCannotSilentlyMixOpenCodeGoCredentialDomains(t *testing.T) {
	for _, child := range []string{"opencode-go-messages", "opencode-go-gemini"} {
		cfg := Config{Proxy: ProxyConfig{URL: "https://family-exception.example.test", Token: strings.Repeat("f", 40), Direct: []string{child}}}
		if err := cfg.materializeProxy(nil); err == nil || !strings.Contains(err.Error(), "opencode-go as a whole") {
			t.Fatalf("partial direct domain accepted for %s: %v", child, err)
		}
	}
}
