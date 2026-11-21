package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/session"
)

func TestWorktreesAcrossPairedTransport(t *testing.T) {
	for _, projects := range []bool{false, true} {
		t.Run(fmt.Sprintf("projects=%t", projects), func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("TERM_LLM_WORKTREE_SETUP", "")
			repo := newGitRepoForBindingTest(t)
			outside := newGitRepoForBindingTest(t)
			path := "/chat/v1/worktrees"
			client, closePeer := mountedPeerServeFixture(t, "/chat", func(s *serveServer) {
				s.startupDir = repo
				s.projectsEnabled = projects
				conversation := &session.Session{ID: "worktree-session", Origin: session.OriginWeb, CWD: repo}
				if projects {
					s.startupDir = t.TempDir()
					p := &session.Project{Name: "Paired", CanonicalDir: repo}
					store, _ := s.projectStore()
					if err := store.CreateProject(context.Background(), p); err != nil {
						t.Fatal(err)
					}
					conversation.ProjectID = p.ID
					path = "/chat/v1/projects/" + p.ID + "/worktrees"
				}
				createDirectorySession(t, s.store, conversation)
			})
			defer closePeer()
			assertPeerResponse(t, client, http.MethodGet, "/chat/v1/capabilities", nil, 200, "application/json", `"worktrees":{"enabled":true}`)
			assertPeerResponse(t, client, http.MethodGet, path, nil, 200, "application/json", repo)
			body := assertPeerResponse(t, client, http.MethodPost, path, strings.NewReader(`{"name":"paired","clean":true}`), 200, "application/json", "paired")
			var data struct{ Worktree worktreeRow }
			if err := json.Unmarshal([]byte(body), &data); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{data.Worktree.Dir, ""} {
				req, err := http.NewRequest(http.MethodPost, "http://peer"+path+"/switch", strings.NewReader(`{"dir":`+mustJSONQuote(dir)+`}`))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(requestSessionIDHeader, "worktree-session")
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				var moved struct {
					WorktreeDir string `json:"worktree_dir"`
					CWD         string `json:"cwd"`
				}
				err = json.NewDecoder(resp.Body).Decode(&moved)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || moved.WorktreeDir != dir || moved.CWD == "" {
					t.Fatalf("switch %q: HTTP %d, workspace=%+v, err=%v", dir, resp.StatusCode, moved, err)
				}
			}
			if err := os.WriteFile(filepath.Join(data.Worktree.Dir, "file.txt"), []byte("peer patch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			assertPeerResponse(t, client, http.MethodGet, path+"/diff?dir="+url.QueryEscape(data.Worktree.Dir), nil, 200, "application/json", "peer patch")
			assertPeerResponse(t, client, http.MethodGet, path+"/diff?dir="+url.QueryEscape(outside), nil, 400, "application/json", "not managed")
			if projects {
				assertPeerResponse(t, client, http.MethodGet, "/chat/v1/worktrees", nil, 403, "application/json", "project_required")
			} else {
				assertPeerResponse(t, client, http.MethodPost, "/chat/v1/responses", strings.NewReader(`{"input":"hello","worktree_dir":`+mustJSONQuote(outside)+`}`), 400, "application/json", "invalid_worktree")
			}
			assertPeerResponse(t, client, http.MethodDelete, path+"?force=1&dir="+url.QueryEscape(data.Worktree.Dir), nil, 200, "application/json", `"ok":true`)
		})
	}
}
