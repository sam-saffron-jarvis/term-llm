package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredReleaseRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/nested/term-llm/term-llm/releases/latest" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Location", "/proxy/nested/term-llm/term-llm/releases/tag/v2.0.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	t.Setenv("TERM_LLM_RELEASE_BASE_URL", server.URL+"/proxy/nested/")
	info, err := FetchLatestRelease(context.Background())
	if err != nil || info.TagName != "v2.0.0" {
		t.Fatalf("info=%v err=%v", info, err)
	}
}
