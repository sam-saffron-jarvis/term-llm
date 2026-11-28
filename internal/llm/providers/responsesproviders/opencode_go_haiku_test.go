package responsesproviders

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestOpenCodeGoHaikuUsesCatalogAndNativeMessages(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		scoped, catalogMissing bool
		requested, want        int
	}{
		{name: "direct", want: 128_000},
		{name: "scoped", scoped: true, want: 128_000},
		{name: "smaller explicit cap", scoped: true, requested: 9000, want: 9000},
		{name: "explicit cap above ceiling", requested: 200_000, want: 128_000},
		{name: "scoped catalog outage", scoped: true, catalogMissing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			serveOpenCodeGoHaikuFixture(t, tc.scoped, tc.catalogMissing, tc.requested, tc.want)
		})
	}
}

func serveOpenCodeGoHaikuFixture(t *testing.T, scoped, catalogMissing bool, requested, want int) {
	t.Helper()
	upstream, recorder := newOpenCodeGoTestServer(t)
	defer upstream.Close()
	prefix, messages := "", "/v1/messages"
	if scoped {
		prefix, messages = "/go", "/go/messages/v1/messages"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case prefix + "/models":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("missing availability credential")
			}
			fmt.Fprint(w, `{"data":[{"id":"claude-haiku-5-5"}]}`)
		case "/api.json":
			if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
				t.Error("public catalog received a credential")
			}
			if catalogMissing {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"opencode-go":{"models":{"claude-haiku-5-5":{"provider":{"npm":"@ai-sdk/anthropic"},"limit":{"context":1000000,"output":128000},"reasoning_options":[{"type":"toggle"},{"type":"effort","values":["high","max"]}]},"not-allowed":{"limit":{"output":128000}}}}}`)
		case messages:
			if r.Header.Get("x-api-key") != "test-key" || r.Header.Get(openCodeGoSessionHeader) == "" {
				t.Error("missing Messages credential or session")
			}
			r.URL.Path = "/v1/messages"
			upstream.Config.Handler.ServeHTTP(w, r)
		case prefix + "/chat/completions":
			if !catalogMissing {
				t.Error("unexpected Chat Completions fallback")
			}
			r.URL.Path = "/chat/completions"
			upstream.Config.Handler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := server.URL + prefix
	var provider *OpenCodeGoProvider
	if scoped {
		if err := proxyclient.Register([]proxyclient.Binding{
			{Root: root, Token: "test-key", RelatedRoots: map[string]string{"opencode-go-messages": root + "/messages", "opencode-go-gemini": root + "/gemini"}},
			{Root: root + "/messages", Token: "test-key"}, {Root: root + "/gemini", Token: "test-key"},
		}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OPENCODE_CATALOG_BASE_URL", server.URL)
		provider = NewOpenCodeGoProviderWithBaseURL("test-key", "", root)
	} else {
		provider = newOpenCodeGoProvider("test-key", "", root, server.URL+"/api.json", server.Client())
	}
	models, err := provider.ListModels(t.Context())
	if err != nil || len(models) != 1 || models[0].ID != opencodeGoHaiku55 {
		t.Fatalf("models = %+v, err = %v", models, err)
	}
	if !catalogMissing && models[0].OutputLimit != 128_000 {
		t.Fatalf("model output limit = %d", models[0].OutputLimit)
	}
	// Reopen the cache to verify that the limit exposed by ListModels survives persistence.
	if !catalogMissing {
		cached, err := readCachedOpenCodeGoModels(provider.catalog.cacheKey)
		if err != nil || cached.models[opencodeGoHaiku55].OutputLimit != 128_000 {
			t.Fatalf("cached metadata = %+v, err = %v", cached, err)
		}
	}
	drainOpenCodeGoStream(t, mustOpenCodeGoStream(t, provider, Request{Model: opencodeGoHaiku55 + "-high", Messages: []Message{UserText("hi")}, MaxOutputTokens: requested}))
	wire := recorder.lastInferenceRequest(t)
	var body struct {
		Model           string `json:"model"`
		MaxTokens       int    `json:"max_tokens"`
		ReasoningEffort string `json:"reasoning_effort"`
		Thinking        struct {
			Type string `json:"type"`
		} `json:"thinking"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal(wire.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != opencodeGoHaiku55 || body.MaxTokens != want {
		t.Fatalf("unexpected request: %s", wire.Body)
	}
	if catalogMissing {
		if wire.Path != "/chat/completions" || body.ReasoningEffort != "high" {
			t.Fatalf("unexpected fallback request: %s %s", wire.Path, wire.Body)
		}
	} else if wire.Path != "/v1/messages" || body.Thinking.Type != "adaptive" || body.OutputConfig.Effort != "high" {
		t.Fatalf("unexpected native request: %s %s", wire.Path, wire.Body)
	}
}
