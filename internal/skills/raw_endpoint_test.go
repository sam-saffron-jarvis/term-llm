package skills

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConstructedRawSkillUsesConfiguredContentRoot(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/proxy/raw/owner/repo/main/SKILL.md" || r.Header.Get("Authorization") != "" {
			t.Errorf("request=%s headers=%v", r.URL, r.Header)
		}
		fmt.Fprint(w, "# Example")
	}))
	defer server.Close()
	t.Setenv("TERM_LLM_GITHUB_RAW_BASE_URL", server.URL+"/proxy/raw/")
	raw, err := RawGitHubURL("https://github.com/owner/repo")
	if err != nil || raw != server.URL+"/proxy/raw/owner/repo/main/SKILL.md" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	content, err := NewRemoteRegistryClientWithKey("not-for-download").DownloadSkill(context.Background(), &RemoteSkill{Repository: "https://github.com/owner/repo"})
	if err != nil || string(content) != "# Example" || !called {
		t.Fatalf("content=%q err=%v called=%v", content, err, called)
	}
}

func TestGeneratedProvenanceKeepsRawGatewayForUpdates(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/team/raw/owner/repo/main/skills/example/SKILL.md" || r.Header.Get("Authorization") != "" {
			t.Errorf("request=%s headers=%v", r.URL, r.Header)
		}
		fmt.Fprint(w, "updated skill")
	}))
	defer server.Close()
	t.Setenv("TERM_LLM_GITHUB_RAW_BASE_URL", server.URL+"/team/raw/")
	skill := DiscoveredSkill{Name: "example", Path: "skills/example", RepoRef: GitHubRepoRef{Owner: "owner", Repo: "repo", Branch: "main"}}
	for _, metadata := range []string{"", "metadata:\n  existing: yes\n"} {
		content, err := InjectGitHubProvenance([]byte("---\nname: original\ndescription: test\n"+metadata+"---\nbody"), skill)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Metadata map[string]string `yaml:"metadata"`
		}
		parts := splitByDelimiter(string(content), "---", 3)
		if err := yaml.Unmarshal([]byte(parts[1]), &parsed); err != nil {
			t.Fatal(err)
		}
		raw := parsed.Metadata["_provenance_raw_url"]
		if raw != server.URL+"/team/raw/owner/repo/main/skills/example/SKILL.md" {
			t.Fatalf("provenance URL=%q", raw)
		}
		if _, err := NewRemoteRegistryClientWithKey("not-for-download").FetchRawURL(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("requests=%d", calls)
	}
}

func TestExplicitStoredRawResourceIsNotRewritten(t *testing.T) {
	called := false
	existing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.RequestURI() != "/signed?sig=a%2Bb" || r.Header.Get("Authorization") != "" {
			t.Errorf("request=%s headers=%v", r.URL, r.Header)
		}
		fmt.Fprint(w, "existing resource")
	}))
	defer existing.Close()
	t.Setenv("TERM_LLM_GITHUB_RAW_BASE_URL", "https://not-used.invalid/team/raw")
	if _, err := NewRemoteRegistryClientWithKey("not-for-download").FetchRawURL(context.Background(), existing.URL+"/signed?sig=a%2Bb"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("stored resource not fetched directly")
	}
}

func TestProvenanceRejectsInvalidRawRoot(t *testing.T) {
	t.Setenv("TERM_LLM_GITHUB_RAW_BASE_URL", "https://user:secret@example.test")
	if _, err := InjectGitHubProvenance([]byte("---\nname: test\n---\nbody"), DiscoveredSkill{}); err == nil || !strings.Contains(err.Error(), "TERM_LLM_GITHUB_RAW_BASE_URL") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
}
