package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestAnthropicStreamingOutputCeiling(t *testing.T) {
	original := ParentHooks.OutputLimit
	ParentHooks.OutputLimit = func(model string) int {
		switch model {
		case "claude-opus-5-5":
			return 64_000 // A cached catalog entry must take precedence.
		case "claude-sonnet-5-5":
			return 128_000
		default:
			return 0
		}
	}
	t.Cleanup(func() { ParentHooks.OutputLimit = original })
	for _, tc := range []struct {
		name     string
		model    string
		search   bool
		gateway  bool
		proxy    bool
		request  int
		catalog  int // 0 = warm cache, -1 = unavailable
		expected int64
	}{
		{name: "high effort", model: "claude-opus-5-5-high", expected: 128_000},
		{name: "native search", model: "claude-opus-5-5-high", search: true, expected: 128_000},
		{name: "curated fallback", model: "claude-sonnet-5-5-high", catalog: -1, expected: 128_000},
		{name: "cold cache fetches catalog", model: "claude-opus-5-5-high", catalog: 96_000, expected: 96_000},
		{name: "explicit lower cap", model: "claude-opus-5-5-high", request: 32_000, expected: 32_000},
		{name: "explicit cap above ceiling", model: "claude-opus-5-5-high", request: 200_000, expected: 128_000},
		{name: "unknown model", model: "claude-unknown-high", expected: 4096},
		{name: "delegated proxy catalog", model: "claude-opus-5-5-high", proxy: true, catalog: 96_000, expected: 96_000},
		{name: "delegated proxy fallback", model: "claude-opus-5-5-high", proxy: true, catalog: -1, expected: 64_000},
		{name: "custom gateway", model: "claude-opus-5-5-high", gateway: true, expected: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.catalog == 0 {
				withAnthropicCache(t, []core.ModelInfo{{ID: "claude-opus-5-5", OutputLimit: 128_000}})
			} else {
				withAnthropicCache(t, nil)
			}
			var got struct {
				MaxTokens    int64                  `json:"max_tokens"`
				Stream       bool                   `json:"stream"`
				OutputConfig *anthropicOutputConfig `json:"output_config"`
			}
			var catalogCalls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.proxy {
					if r.Header.Get("X-Api-Key") != "test-key" {
						t.Error("missing route credential")
					}
					if r.URL.Path != "/anthropic-work/v1/models" && r.URL.Path != "/anthropic-work/v1/messages" {
						t.Errorf("unexpected route path %s", r.URL.Path)
					}
				}
				if r.URL.Path == "/v1/models" || r.URL.Path == "/anthropic-work/v1/models" {
					catalogCalls++
					if tc.catalog <= 0 {
						http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
						return
					}
					fmt.Fprintf(w, `{"data":[{"id":"claude-opus-5-5","max_tokens":%d}]}`, tc.catalog)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, minimalAnthropicSSE())
			}))
			defer srv.Close()
			baseURL := anthropicDefaultBaseURL
			if tc.gateway {
				baseURL = srv.URL
			}
			if tc.proxy {
				baseURL = srv.URL + "/anthropic-work"
				if err := proxyclient.Register([]proxyclient.Binding{{Root: baseURL, Token: "test-key"}}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := NewAnthropicProviderWithBaseURL("test-key", tc.model, AnthropicCredAPIKey, baseURL)
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			p.client.httpClient.Transport = goldenRedirectTransport{target: target, base: srv.Client().Transport}
			stream, err := p.Stream(context.Background(), core.Request{
				Messages: []core.Message{core.UserText("hi")}, Search: tc.search, MaxOutputTokens: tc.request,
			})
			if err != nil {
				t.Fatal(err)
			}
			events := drainGoldenStream(t, stream)
			if len(events) == 0 || events[len(events)-1].Type != core.EventDone {
				t.Fatalf("unexpected events: %+v", events)
			}
			wantCatalogCalls := 0
			if tc.catalog != 0 {
				wantCatalogCalls = 1
			}
			if catalogCalls != wantCatalogCalls {
				t.Errorf("GET /v1/models calls = %d, want %d", catalogCalls, wantCatalogCalls)
			}
			if got.MaxTokens != tc.expected || !got.Stream || got.OutputConfig == nil || got.OutputConfig.Effort != "high" {
				t.Fatalf("max_tokens = %d, stream = %v, output_config = %+v; want %d, true, high", got.MaxTokens, got.Stream, got.OutputConfig, tc.expected)
			}
		})
	}
}
