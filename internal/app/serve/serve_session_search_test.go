package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/session"
)

type searchRequestStore struct {
	session.Store
	options []session.SearchOptions
	results []session.SearchResult
}

func (s *searchRequestStore) Search(_ context.Context, opts session.SearchOptions) ([]session.SearchResult, error) {
	s.options = append(s.options, opts)
	results := append([]session.SearchResult(nil), s.results...)
	if opts.OmitSnippet {
		for i := range results {
			results[i].Snippet = ""
		}
	}
	return results, nil
}

func TestHandleSessionsSearchSort(t *testing.T) {
	for _, tc := range []struct {
		query  string
		want   session.SearchSort
		status int
	}{
		{"", "", http.StatusOK},
		{"&sort=relevance", session.SearchSortRelevance, http.StatusOK},
		{"&sort=newest", session.SearchSortNewest, http.StatusOK},
		{"&sort=oldest", session.SearchSortOldest, http.StatusOK},
		{"&sort=invalid", "", http.StatusBadRequest},
		{"&sort=updated_at%20DESC", "", http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			store := &searchRequestStore{}
			srv := &serveServer{store: store}
			req := httptest.NewRequest(http.MethodGet, "/v1/sessions/search?q=needle&limit=99&categories=web&include_archived=1&project_id=p1"+tc.query, nil)
			rr := httptest.NewRecorder()
			srv.handleSessionsSearch(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d; body = %s", rr.Code, rr.Body.String())
			}
			if tc.status != http.StatusOK {
				if len(store.options) != 0 {
					t.Fatal("invalid sort reached store")
				}
				return
			}
			if len(store.options) != 1 {
				t.Fatalf("search calls = %d", len(store.options))
			}
			opts := store.options[0]
			if opts.Sort != tc.want || opts.Limit != 50 || opts.Query != "needle" || !opts.Archived || !opts.ExcludeSubagents || opts.ProjectID != "p1" || len(opts.Categories) != 1 || opts.Categories[0] != "web" {
				t.Fatalf("unexpected options: %#v", opts)
			}
		})
	}
}

func TestHandleSessionsSearchSnippetOptOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		omit bool
	}{
		{name: "default"},
		{name: "enabled", arg: "&include_snippet=1"},
		{name: "unrecognized", arg: "&include_snippet=invalid"},
		{name: "disabled", arg: "&include_snippet=0", omit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const snippet = "a matching excerpt"
			store := &searchRequestStore{results: []session.SearchResult{{
				SessionID: "match", ProviderKey: "mock", MessageID: 42, Snippet: snippet,
			}}}
			srv := &serveServer{store: store}
			req := httptest.NewRequest(http.MethodGet, "/v1/sessions/search?q=needle"+tc.arg, nil)
			rr := httptest.NewRecorder()
			srv.handleSessionsSearch(rr, req)
			if rr.Code != http.StatusOK || len(store.options) != 1 || store.options[0].OmitSnippet != tc.omit {
				t.Fatalf("status = %d, options = %#v, body = %s", rr.Code, store.options, rr.Body.String())
			}
			var body struct {
				Sessions []struct {
					ID        string          `json:"id"`
					MessageID int64           `json:"message_id"`
					Snippet   json.RawMessage `json:"snippet"`
				} `json:"sessions"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Sessions) != 1 || body.Sessions[0].ID != "match" || body.Sessions[0].MessageID != 42 {
				t.Fatalf("unexpected matches: %s", rr.Body.String())
			}
			want := `"` + snippet + `"`
			if tc.omit {
				want = ""
			}
			if got := string(body.Sessions[0].Snippet); got != want {
				t.Fatalf("snippet JSON = %q, want %q", got, want)
			}
		})
	}
}

func TestHandleSessionsSearchPreservesTrailingWhitespace(t *testing.T) {
	store := &searchRequestStore{}
	srv := &serveServer{store: store}
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/search?q=discourse+githu+", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionsSearch(rr, req)
	if rr.Code != http.StatusOK || len(store.options) != 1 || store.options[0].Query != "discourse githu " {
		t.Fatalf("status = %d, options = %#v", rr.Code, store.options)
	}
}

func TestHandleSessionsSearchPrefixOptIn(t *testing.T) {
	for _, param := range []string{"", "&prefix=0", "&prefix=invalid", "&prefix=1"} {
		store := &searchRequestStore{}
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/search?q=design+form"+param, nil)
		rr := httptest.NewRecorder()
		(&serveServer{store: store}).handleSessionsSearch(rr, req)
		if rr.Code != http.StatusOK || len(store.options) != 1 || store.options[0].Prefix != (param == "&prefix=1") {
			t.Fatalf("%q: status %d, options %#v", param, rr.Code, store.options)
		}
	}
}
