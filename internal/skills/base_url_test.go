package skills

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAncillaryConfiguredRoots(t *testing.T) {
	const query = "x & café?#"
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/proxy/api/repos/owner/repo/contents/skills":
			if r.URL.Query().Get("ref") != "branch&special" {
				t.Errorf("ref=%q", r.URL.RawQuery)
			}
			if r.Header.Get("Authorization") != "Bearer fake-token" {
				t.Error("missing GitHub credential")
			}
			fmt.Fprint(w, `[]`)
		case "/proxy/raw/owner/repo/main/skills/example/SKILL.md":
			if r.Header.Get("Authorization") != "" {
				t.Error("GitHub token sent to raw content origin")
			}
			fmt.Fprint(w, "# Example")
		case "/proxy/registry/skills/search":
			if r.URL.Query().Get("q") != query {
				t.Errorf("q=%q", r.URL.RawQuery)
			}
			if r.Header.Get("Authorization") != "Bearer registry-key" {
				t.Error("missing registry credential")
			}
			fmt.Fprint(w, `{"success":true,"data":{"skills":[]}}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL+"/proxy/api/")
	t.Setenv("TERM_LLM_GITHUB_RAW_BASE_URL", server.URL+"/proxy/raw/")
	t.Setenv("SKILLSMP_BASE_URL", server.URL+"/proxy/registry/")
	t.Setenv("GITHUB_TOKEN", "fake-token")
	client := NewGitHubClient()
	if _, err := client.fetchContents(context.Background(), server.URL+"/proxy/api/repos/owner/repo/contents/skills?ref=branch%26special"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.countFiles(context.Background(), GitHubRepoRef{Owner: "owner", Repo: "repo"}, "skills", "branch&special"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchSkillMD(context.Background(), DiscoveredSkill{Path: "skills/example", RepoRef: GitHubRepoRef{Owner: "owner", Repo: "repo", Branch: "main"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRemoteRegistryClientWithKey("registry-key").Search(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls=%v", calls)
	}
}
