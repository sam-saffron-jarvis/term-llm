package responsesproviders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestScopedOpenCodeGoRoutesEveryNativeProtocolWithItsOwnAuth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	upstream, _ := newOpenCodeGoTestServer(t)
	defer upstream.Close()
	type observation struct {
		path   string
		header http.Header
	}
	var mu sync.Mutex
	observed := []observation{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observed = append(observed, observation{r.URL.Path, r.Header.Clone()})
		mu.Unlock()
		for _, prefix := range []string{"/household-messages", "/household-gemini", "/household"} {
			if strings.HasPrefix(r.URL.Path, prefix+"/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
				break
			}
		}
		upstream.Config.Handler.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	root := gateway.URL + "/household"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Token: "scoped-family"}, {Root: root + "-messages", Token: "scoped-family"}, {Root: root + "-gemini", Token: "scoped-family"}}); err != nil {
		t.Fatal(err)
	}
	provider := newOpenCodeGoProvider("scoped-family", "", root, "", gateway.Client())
	provider.catalog.store(map[string]opencodeGoModel{
		"chat-model":      {ModelInfo: ModelInfo{ID: "chat-model"}, Protocol: opencodeGoProtocolChat, Available: true},
		"messages-model":  {ModelInfo: ModelInfo{ID: "messages-model"}, Protocol: opencodeGoProtocolMessages, Available: true},
		"responses-model": {ModelInfo: ModelInfo{ID: "responses-model"}, Protocol: opencodeGoProtocolResponses, Available: true},
		"gemini-model":    {ModelInfo: ModelInfo{ID: "gemini-model"}, Protocol: opencodeGoProtocolGemini, Available: true},
	}, time.Now(), time.Now(), nil)
	cases := []struct{ model, path, header, value string }{
		{"chat-model", "/household/chat/completions", "Authorization", "Bearer scoped-family"},
		{"messages-model", "/household-messages/v1/messages", "x-api-key", "scoped-family"},
		{"responses-model", "/household/responses", "Authorization", "Bearer scoped-family"},
		{"gemini-model", "/household-gemini/models/gemini-model:streamGenerateContent", "x-goog-api-key", "scoped-family"},
	}
	for _, tc := range cases {
		drainOpenCodeGoStream(t, mustOpenCodeGoStream(t, provider, Request{Model: tc.model, Messages: []Message{UserText("synthetic protocol fixture")}}))
		mu.Lock()
		got := observed[len(observed)-1]
		mu.Unlock()
		if got.path != tc.path || got.header.Get(tc.header) != tc.value {
			t.Fatalf("%s used path %s / wrong scoped auth", tc.model, got.path)
		}
	}
}
func TestScopedOpenCodeGoMissingProtocolBindingFailsClosed(t *testing.T) {
	root := "https://opencode-missing.example.test/only-root"
	if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Token: "scoped"}}); err != nil {
		t.Fatal(err)
	}
	provider := NewOpenCodeGoProviderWithBaseURL("scoped", "gemini-model", root)
	if provider.proxyError == nil {
		t.Fatal("missing sibling silently reused a wrong or public root")
	}
}
