package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

// clearProviderCredentialEnv blanks every environment variable that can
// enable a built-in provider, so tests do not depend on the developer's shell.
func clearProviderCredentialEnv(t *testing.T) {
	t.Helper()
	for _, spec := range config.BuiltinProviders() {
		for _, v := range spec.EnablingEnv() {
			t.Setenv(v, "")
		}
	}
}

func providerNames(t *testing.T, srv *serveServer) []string {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.handleProviders(rr, httptest.NewRequest(http.MethodGet, "/v1/providers", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var result struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	names := make([]string, 0, len(result.Data))
	for _, p := range result.Data {
		names = append(names, p.Name)
	}
	return names
}

func TestHandleProviders_ListsOnlyConfiguredProviders(t *testing.T) {
	clearProviderCredentialEnv(t)
	t.Setenv("XAI_API_KEY", "xai-test")
	t.Setenv("VLLM_API_KEY", "vllm-test") // no default endpoint: must not enable vllm

	enabled, disabled := true, false
	srv := &serveServer{cfgRef: &config.Config{
		DefaultProvider: "zen",
		Providers: map[string]config.ProviderConfig{
			"openai":    {Model: "gpt-5"},
			"anthropic": {Model: "claude-sonnet-4-6", FromDefaults: true}, // schema default only
			"copilot":   {Enabled: &disabled},                             // signed in, but disabled
			"old-llm":   {Type: config.ProviderTypeOpenAICompat, Enabled: &disabled},
			"xai":       {Enabled: &enabled},
			"corp-llm":  {Type: config.ProviderTypeOpenAICompat, Model: "m"},
			"local-gpu": {Type: config.ProviderTypeVLLM, Model: "q"},
		},
	}}
	srv.providerCreds.Detect = func(name string) bool { return name == "claude-bin" || name == "copilot" }

	got := providerNames(t, srv)
	want := []string{"claude-bin", "corp-llm", "local-gpu", "openai", "xai", "zen"}
	if !slices.Equal(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	if !srv.offersProvider("xai") || srv.offersProvider("anthropic") || srv.offersProvider("vllm") || srv.offersProvider("copilot") {
		t.Fatal("offersProvider disagrees with /v1/providers")
	}
}

func TestHandleProviders_ProxyAllOmitsUnavailableProviders(t *testing.T) {
	clearProviderCredentialEnv(t)
	const token = "test-token-for-serve-providers"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/_proxy/client/v2/providers" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("proxy catalog request did not use the fixture token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"providers":[{"id":"openai-work","family":"inference","routes":[{"name":"production","profile":"openai","local_prefix":"/openai-work/"}]}]}`)
	}))
	t.Cleanup(proxy.Close)

	// Omitting proxy.providers selects legacy proxy-all, even though the
	// caller's catalog grants just one named inference route.
	writeServeContractConfig(t, "proxy:\n  url: "+proxy.URL+"\n  token: "+token+"\n")
	cfg, err := config.LoadWithoutCredentials()
	if err != nil {
		t.Fatal(err)
	}
	srv := &serveServer{cfgRef: cfg}
	srv.providerCreds.Detect = func(string) bool { return false }

	names := providerNames(t, srv)
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"openai-work", true},
		{"bedrock", false},
		{"vibe-bin", false},
		{"cursor-bin", false},
	} {
		if got := slices.Contains(names, tc.name); got != tc.want {
			t.Errorf("/v1/providers contains %q = %t, want %t", tc.name, got, tc.want)
		}
		if got := srv.offersProvider(tc.name); got != tc.want {
			t.Errorf("offersProvider(%q) = %t, want %t", tc.name, got, tc.want)
		}
	}
}
