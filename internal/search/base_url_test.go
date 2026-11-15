package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestFactoryBaseURLRequests(t *testing.T) {
	for _, trailing := range []string{"", "/"} {
		for _, provider := range []string{"exa", "perplexity", "parallel", "tavily", "brave", "google", "youcom", "youcom-keyless", "duckduckgo"} {
			t.Run(provider+trailing, func(t *testing.T) {
				query := "café & x=?/# +雪"
				called := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called++
					suffix := "/search"
					switch provider {
					case "brave":
						suffix = "/web/search"
					case "google":
						suffix = ""
					case "youcom-keyless":
						suffix = "/agents/search"
					case "duckduckgo":
						suffix = "/lite/"
					}
					if r.URL.Path != "/gateway/nested/v1"+suffix {
						t.Errorf("path=%s", r.URL.Path)
					}
					switch provider {
					case "brave", "google", "duckduckgo":
						if r.URL.Query().Get("q") != query {
							t.Errorf("query=%q", r.URL.RawQuery)
						}
					case "youcom-keyless":
						if r.URL.Query().Get("query") != query {
							t.Errorf("query=%q", r.URL.RawQuery)
						}
					default:
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if provider == "parallel" {
							if fmt.Sprint(body["search_queries"]) != "["+query+"]" {
								t.Errorf("body=%v", body)
							}
						} else if body["query"] != query {
							t.Errorf("body=%v", body)
						}
					}
					if provider == "duckduckgo" {
						fmt.Fprint(w, "<html></html>")
						return
					}
					if strings.HasPrefix(provider, "youcom") {
						fmt.Fprint(w, `{"results":{"web":[],"news":[]}}`)
						return
					}
					fmt.Fprint(w, `{"results":[],"web":{"results":[]},"items":[]}`)
				}))
				defer server.Close()
				base := server.URL + "/gateway/nested/v1" + trailing
				t.Setenv("AUDIT_SEARCH_BASE", base)
				cfg := &config.Config{Search: config.SearchConfig{Provider: provider}}
				raw := "${AUDIT_SEARCH_BASE}"
				switch provider {
				case "exa":
					cfg.Search.Exa = config.SearchExaConfig{APIKey: "test", BaseURL: raw}
				case "perplexity":
					cfg.Search.Perplexity = config.SearchPerplexityConfig{APIKey: "test", BaseURL: raw}
				case "parallel":
					cfg.Search.Parallel = config.SearchParallelConfig{APIKey: "test", BaseURL: raw}
				case "tavily":
					cfg.Search.Tavily = config.SearchTavilyConfig{APIKey: "test", BaseURL: raw}
				case "brave":
					cfg.Search.Brave = config.SearchBraveConfig{APIKey: "test", BaseURL: raw}
				case "google":
					cfg.Search.Google = config.SearchGoogleConfig{APIKey: "test", CX: "test-cx", BaseURL: raw}
				case "youcom":
					cfg.Search.YouCom = config.SearchYouComConfig{APIKey: "test", BaseURL: raw}
				case "youcom-keyless":
					t.Setenv("YDC_API_KEY", "")
					cfg.Search.Provider = "youcom"
					cfg.Search.YouCom.BaseURL = raw
				case "duckduckgo":
					cfg.Search.DuckDuckGo.BaseURL = raw
				}
				searcher, err := NewSearcher(cfg)
				if err != nil {
					t.Fatal(err)
				}
				_, err = searcher.Search(context.Background(), query, 3)
				if err != nil {
					t.Fatal(err)
				}
				if called != 1 {
					t.Fatalf("requests=%d", called)
				}
			})
		}
	}
}

func TestFactoryBaseURLFailures(t *testing.T) {
	for _, raw := range []string{"file:///tmp/no", "https://user:secret@example.test/api", "https://example.test/api?key=secret", "https://example.test/#fragment"} {
		cfg := &config.Config{Search: config.SearchConfig{Provider: "exa", Exa: config.SearchExaConfig{APIKey: "test", BaseURL: raw}}}
		_, err := NewSearcher(cfg)
		if err == nil || !strings.Contains(err.Error(), "search.exa.base_url") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("error=%v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); fmt.Fprint(w, "limit") }))
	defer server.Close()
	cfg := &config.Config{Search: config.SearchConfig{Provider: "exa", Exa: config.SearchExaConfig{APIKey: "test", BaseURL: server.URL + "/proxy"}}}
	searcher, err := NewSearcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = searcher.Search(context.Background(), "query", 2); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error=%v", err)
	}
}
