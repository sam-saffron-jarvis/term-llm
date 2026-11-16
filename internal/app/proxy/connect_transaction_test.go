package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestConnectAtomicFailureRollsBackAllEffectsAndCanRecover(t *testing.T) {
	s := testService(t)
	input := connectInput(81, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	revision := s.snap.revision
	if _, err := s.store.db.Exec("CREATE TRIGGER stop_connect_grant BEFORE INSERT ON grants BEGIN SELECT RAISE(FAIL,'synthetic atomic connect failure'); END"); err != nil {
		t.Fatal(err)
	}
	rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: "synthetic-never-in-receipt"})
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "synthetic-never-in-receipt") {
		t.Fatal("atomic failure leaked token or claimed success", rr.Code, rr.Body.String())
	}
	assertConnectCounts(t, s, 0, 0, 0, 0)
	if s.snap.revision != revision {
		t.Fatal("failed commit activated route")
	}
	var status string
	if err := s.store.db.QueryRow("SELECT status FROM connect_requests WHERE id=?", input.ID).Scan(&status); err != nil || status != "approved" {
		t.Fatal("failed receipt lost recoverable approval", status, err)
	}
	if _, err := s.store.db.Exec("DROP TRIGGER stop_connect_grant"); err != nil {
		t.Fatal(err)
	}
	complete := commitConnectTest(t, s, input, "synthetic-never-in-receipt")
	if complete.Result == nil || complete.Result.Token == "" {
		t.Fatal("uncertain result could not recover")
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}

func TestConnectApprovedGenerationAndPolicyDriftNeverWidens(t *testing.T) {
	for _, change := range []string{"generation", "policy"} {
		t.Run(change, func(t *testing.T) {
			s := testService(t)
			first := connectInput(91, "claude", "Original", "auto")
			approveConnectTest(t, s, first)
			old := commitConnectTest(t, s, first, "synthetic-original")
			request := connectInput(92, "claude", "New device", "reconnect")
			approveConnectTest(t, s, request)
			cookie, csrf := loginAdmin(t, s)
			if change == "generation" {
				rr := adminRequest(s, http.MethodPut, "accounts", accountInput{Name: "claude", Kind: "claude-code", Replace: true, Credentials: accountCredentials{Mode: "token_only", AccessToken: "synthetic-explicit-admin"}}, cookie, csrf)
				if rr.Code != 200 {
					t.Fatal(rr.Code, rr.Body.String())
				}
			} else {
				r := s.snap.config.Routes[0]
				r.Allow = append(r.Allow, "GET /changed-scope")
				configure(t, s, Config{Routes: []Route{r}}, nil)
			}
			rr := connectCall(s, "POST", "commit", request.Secret, connectCommitInput{ID: request.ID, Token: "synthetic-must-not-apply"})
			if rr.Code != 409 {
				t.Fatal("stale approval committed", rr.Code, rr.Body.String())
			}
			var callers int
			s.store.db.QueryRow("SELECT count(*) FROM callers").Scan(&callers)
			if callers != 1 {
				t.Fatal("conflict created a caller")
			}
			a, err := s.store.account("claude")
			if err != nil || a.credentials.AccessToken == "synthetic-must-not-apply" {
				t.Fatal("conflict changed credentials", err)
			}
			if change == "generation" {
				if _, err = s.store.authorize(old.Result.Token, s.snap.routes[0]); err != nil {
					t.Fatal("reconnect conflict removed old grant", err)
				}
			}
		})
	}
}

func TestConnectRestartPreservesSameReceiptAndDeviceDoesNotReplaceAccount(t *testing.T) {
	s := testService(t)
	input := connectInput(101, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	complete := commitConnectTest(t, s, input, "synthetic-original")
	opts := s.opts
	s.Close()
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	recovered := commitConnectTest(t, next, input, "ignored-on-recovery")
	if recovered.Result.Token != complete.Result.Token || recovered.Result.CallerID != complete.Result.CallerID {
		t.Fatal("restart created another result")
	}
	a, err := next.store.account("claude")
	if err != nil || a.Generation != 1 || a.credentials.AccessToken != "synthetic-original" {
		t.Fatal("receipt replay replaced account", err)
	}
	device := connectInput(102, "claude", "Second", "auto")
	if approved := approveConnectTest(t, next, device); approved.ResolvedAction != "device" {
		t.Fatal("managed account not recognized after restart", approved)
	}
	commitConnectTest(t, next, device, "")
	a, err = next.store.account("claude")
	if err != nil || a.Generation != 1 {
		t.Fatal("device changed account", err)
	}
	assertConnectCounts(t, next, 1, 1, 2, 2)
}

func TestConnectCompetingApprovedAddsNeverFallbackOrOverwrite(t *testing.T) {
	s := testService(t)
	first := connectInput(701, "claude", "First", "auto")
	second := connectInput(702, "claude", "Second", "auto")
	approveConnectTest(t, s, first)
	approveConnectTest(t, s, second)
	commitConnectTest(t, s, first, "synthetic-first")
	rr := connectCall(s, "POST", "commit", second.Secret, connectCommitInput{ID: second.ID, Token: "synthetic-must-not-replace"})
	if rr.Code != 409 {
		t.Fatal("competing approval did not conflict", rr.Code, rr.Body.String())
	}
	a, err := s.store.account("claude")
	if err != nil || a.credentials.AccessToken != "synthetic-first" || a.Generation != 1 {
		t.Fatal("competing add overwrote account", err)
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}
