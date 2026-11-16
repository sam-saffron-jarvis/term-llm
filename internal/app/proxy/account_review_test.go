package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func expireAccountBackoff(t *testing.T, s *Service) {
	t.Helper()
	a, e := s.store.account("account")
	if e != nil {
		t.Fatal(e)
	}
	a.refresh.RetryAfter = 0
	if e = s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
		t.Fatal(e)
	}
}

func TestAccountSafeDialFailureRetriesWithoutConsumingRefresh(t *testing.T) {
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	realDial := s.transport.DialContext
	s.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	if rr := accountCall(s, token); rr.Code != 502 {
		t.Fatal(rr.Code)
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "ready" || a.Generation != 1 || a.credentials.RefreshToken != "synthetic-old-refresh" || a.refresh.RetryAfter <= time.Now().UnixMilli() {
		t.Fatal("safe dial failure destroyed custody", a.AccountInfo, e)
	}
	s.transport.DialContext = realDial
	if rr := accountCall(s, token); rr.Code != 502 || issuer.Load() != 0 {
		t.Fatal("backoff did not bound retry", rr.Code, issuer.Load())
	}
	expireAccountBackoff(t, s)
	if rr := accountCall(s, token); rr.Code != 200 || issuer.Load() != 1 || inference.Load() != 1 {
		t.Fatal("safe failure could not retry original credential", rr.Code, issuer.Load(), inference.Load())
	}
}

func TestAccountSentRefreshWithoutReplyRemainsNeedsLogin(t *testing.T) {
	var sent atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.Copy(io.Discard, r.Body)
		c, _, e := http.NewResponseController(w).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		c.Close()
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	for range 2 {
		if rr := accountCall(s, token); rr.Code != 502 {
			t.Fatal(rr.Code)
		}
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "needs_login" || sent.Load() != 1 {
		t.Fatal("uncertain sent refresh replayed", a.State, e, sent.Load())
	}
}

func TestAccountCopilotFailuresBackOffWithoutDestroyingCredential(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				io.WriteString(w, `{"error":"synthetic"}`)
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, "copilot", up, true)
			token := configureAccount(t, s, accountRoute(up.URL))
			for range 2 {
				if rr := accountCall(s, token); rr.Code != 502 {
					t.Fatal(rr.Code)
				}
			}
			a, e := s.store.account("account")
			if e != nil || a.State != "ready" || a.credentials.GitHubToken != "synthetic-github-token" || a.refresh.Failures != 1 || calls.Load() != 1 {
				t.Fatal("nonrotating exchange permanently disabled", a.AccountInfo, e, calls.Load())
			}
			// Persisted cooldown survives restart; the long-lived token is not consumed.
			opts := s.opts
			s.Close()
			next, e := Open(opts)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if _, e = next.ensureAccount(context.Background(), "account", 0); e == nil || calls.Load() != 1 {
				t.Fatal("restart lost issuer backoff")
			}
			next.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			for range 7 {
				expireAccountBackoff(t, next)
				if _, e = next.ensureAccount(context.Background(), "account", 0); e == nil {
					t.Fatal("issuer failure accepted")
				}
			}
			a, e = next.store.account("account")
			if e != nil || a.State != "ready" || a.refresh.RetryAfter > time.Now().Add(accountRetryMaximum+time.Second).UnixMilli() {
				t.Fatal("unbounded retry backoff", e)
			}
		})
	}
}

func TestAccountAWS401PreservesStaticKeys(t *testing.T) {
	var inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inference.Add(1)
		w.WriteHeader(401)
		io.WriteString(w, "rejected request shape")
	}))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "aws-sigv4", up, false)
	a.ExpiresAt = 0
	a.operator.Region = "us-west-2"
	a.credentials = accountCredentials{AccessKeyID: "synthetic-key", SecretAccessKey: "synthetic-secret"}
	if e := s.store.replaceAccount(context.Background(), a, 1); e != nil {
		t.Fatal(e)
	}
	r := accountRoute(up.URL)
	r.Upstream = up.URL
	r.Allow = []string{"POST /model/"}
	token := configureAccount(t, s, r)
	for range 2 {
		req := httptest.NewRequest("POST", "/account/model/test/invoke", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 401 || rr.Header().Get("X-Term-LLM-Proxy-Upstream-Auth") != "refresh-limited" {
			t.Fatal(rr.Code)
		}
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "ready" || a.Generation != 1 || a.credentials.AccessKeyID != "synthetic-key" || inference.Load() != 2 {
		t.Fatal("AWS rejection destroyed signing credentials", a.AccountInfo, e, inference.Load())
	}
}

func TestAccountSequential401RefreshIntervalAndNoReplay(t *testing.T) {
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		w.WriteHeader(401)
		io.WriteString(w, "original authentication error")
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, false)
	token := configureAccount(t, s, accountRoute(up.URL))
	for i := range 2 {
		rr := accountCall(s, token)
		if rr.Code != 401 || rr.Body.String() != "original authentication error" {
			t.Fatal("401 was replayed/replaced", rr.Code, rr.Body.String())
		}
		want := "refresh-limited"
		if i == 0 {
			want = "refreshed"
		}
		if rr.Header().Get("X-Term-LLM-Proxy-Upstream-Auth") != want {
			t.Fatal(rr.Header())
		}
	}
	for range 2 {
		if rr := accountCall(s, token); rr.Code != 502 {
			t.Fatal("known rejected token reused during cooldown", rr.Code)
		}
	}
	if issuer.Load() != 1 || inference.Load() != 2 {
		t.Fatal("sequential 401 consumed rotating tokens", issuer.Load(), inference.Load())
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "ready" || a.Generation != 2 {
		t.Fatal(a.AccountInfo, e)
	}
	a.refresh.LastSuccess = time.Now().Add(-accountRefreshInterval - time.Second).UnixMilli()
	if e = s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
		t.Fatal(e)
	}
	rr := accountCall(s, token)
	if rr.Code != 401 || issuer.Load() != 2 || inference.Load() != 3 {
		t.Fatal("rotation did not resume after interval", rr.Code, issuer.Load(), inference.Load())
	}
}

func TestAccountPredispatchEvidenceNeverMarksAttemptedInferenceFree(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		t.Run(fmt.Sprint(attempted), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				c, _, e := http.NewResponseController(w).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				c.Close()
			}))
			defer up.Close()
			s := testService(t)
			a := testAccount(t, s, "chatgpt", up, false)
			token := configureAccount(t, s, accountRoute(up.URL))
			if !attempted {
				a.State = "needs_login"
				if e := s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
					t.Fatal(e)
				}
			}
			rr := accountCall(s, token)
			if rr.Code != 502 {
				t.Fatal(rr.Code)
			}
			records, _, e := s.store.records(nil, 5)
			if e != nil || len(records) != 1 {
				t.Fatal(e)
			}
			if records[0].Money == nil || (records[0].Money.Kind == "no_charge_expected") == attempted {
				t.Fatal("dispatch evidence not reflected in money", attempted, records[0].Money)
			}
			if (records[0].Outcome == "not_dispatched") == attempted {
				t.Fatal("incorrect dispatch evidence", attempted, records[0].Outcome)
			}
			want := int32(0)
			if attempted {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("unexpected inference calls", calls.Load())
			}
		})
	}
}

func TestAccountSSERevocationReleasesLiveUse(t *testing.T) {
	for _, action := range []string{"caller", "grant", "account", "route"} {
		t.Run(action, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: admitted\n\n")
				http.NewResponseController(w).Flush()
				<-r.Context().Done()
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, "chatgpt", up, false)
			r := accountRoute(up.URL)
			r.Usage = "auto"
			token := configureAccount(t, s, r)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); s.ServeHTTP(w, r) }))
			defer server.Close()
			req, _ := http.NewRequest("POST", server.URL+"/account/responses", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, e := server.Client().Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			reader := bufio.NewReader(resp.Body)
			if _, e = reader.ReadString('\n'); e != nil {
				t.Fatal(e)
			}
			callers, e := s.store.callers()
			if e != nil || len(callers) != 1 {
				t.Fatal(e)
			}
			cookie, csrf := loginAdmin(t, s)
			var rr *httptest.ResponseRecorder
			switch action {
			case "caller":
				rr = adminRequest(s, "DELETE", "callers", map[string]string{"id": callers[0].ID}, cookie, csrf)
			case "grant":
				rr = adminRequest(s, "PUT", "callers", map[string]any{"id": callers[0].ID, "grants": []string{}}, cookie, csrf)
			case "account":
				rr = adminRequest(s, "DELETE", "accounts", map[string]string{"name": "account"}, cookie, csrf)
			case "route":
				r.Enabled = false
				rr = adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{r}}, cookie, csrf)
			}
			if rr.Code != 200 {
				t.Fatal(rr.Code, rr.Body.String())
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("revoked SSE retained admission")
			}
			if len(s.slots) != 0 {
				t.Fatal("SSE slot leaked")
			}
			records, _, e := s.store.records(nil, 5)
			if e != nil || len(records) != 1 || records[0].Outcome != "closed_by_revocation" {
				t.Fatal("SSE revocation ledger", e, records)
			}
		})
	}
}

func TestAccountHTTP2SentRefreshFailureIsUncertain(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ProtoMajor != 2 {
			t.Error("issuer fixture did not negotiate HTTP/2")
		}
		io.Copy(io.Discard, r.Body)
		panic(http.ErrAbortHandler)
	}))
	up.EnableHTTP2 = true
	up.StartTLS()
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	for range 2 {
		if rr := accountCall(s, token); rr.Code != 502 {
			t.Fatal(rr.Code)
		}
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "needs_login" || calls.Load() != 1 {
		t.Fatal("HTTP/2 uncertain refresh retried", a.State, e, calls.Load())
	}
}

func TestAccountCopilotInterruptedFenceCanExchangeAgain(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"token":"synthetic-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "copilot", up, true)
	if e := s.store.fenceAccount(context.Background(), "account", 1); e != nil {
		t.Fatal(e)
	}
	opts := s.opts
	s.Close()
	next, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	next.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	a, e := next.ensureAccount(context.Background(), "account", 0)
	if e != nil || a.State != "ready" || a.Generation != 2 || calls.Load() != 1 {
		t.Fatal("nonrotating credential fenced permanently after restart", a.AccountInfo, e, calls.Load())
	}
}

func TestAccountSSERevokedWhileWaitingForHeadersCannotBecomeLive(t *testing.T) {
	for _, action := range []string{"caller", "replacement", "account", "needs_login", "routine_rotation"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: late\n\n")
			}))
			defer up.Close()
			s := testService(t)
			account := testAccount(t, s, "chatgpt", up, false)
			token := configureAccount(t, s, accountRoute(up.URL))
			done := make(chan struct{})
			go func() { defer close(done); accountCall(s, token) }()
			<-entered
			cookie, csrf := loginAdmin(t, s)
			var rr *httptest.ResponseRecorder
			switch action {
			case "caller":
				callers, e := s.store.callers()
				if e != nil || len(callers) != 1 {
					close(release)
					t.Fatal(e)
				}
				rr = adminRequest(s, "DELETE", "callers", map[string]string{"id": callers[0].ID}, cookie, csrf)
			case "replacement":
				rr = adminRequest(s, "PUT", "accounts", accountInput{Name: account.Name, Kind: account.Kind, Replace: true, Credentials: account.credentials}, cookie, csrf)
			case "account":
				rr = adminRequest(s, "DELETE", "accounts", map[string]string{"name": account.Name}, cookie, csrf)
			case "needs_login":
				s.failAccount(context.Background(), account)
			case "routine_rotation":
				account.Generation++
				account.refresh.LastSuccess = time.Now().UnixMilli()
				if e := s.store.replaceAccount(context.Background(), account, 1); e != nil {
					close(release)
					t.Fatal(e)
				}
			}
			close(release)
			if rr != nil && rr.Code != 200 {
				t.Fatal(rr.Code)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("late SSE became uncancellable")
			}
			want := "closed_by_revocation"
			if action == "routine_rotation" {
				want = "complete"
			}
			records, _, e := s.store.records(nil, 5)
			if e != nil || len(records) != 1 || records[0].Outcome != want {
				t.Fatal("late response policy", records, e)
			}
		})
	}
}

func TestAccountCopilotFailedResultAndRecoveryCommitDoesNotRequireLogin(t *testing.T) {
	var issuer atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			fmt.Fprintf(w, `{"token":"synthetic-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
			return
		}
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	testAccount(t, s, "copilot", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	if _, e := s.store.db.Exec("CREATE TRIGGER fail_copilot_ready BEFORE UPDATE ON accounts WHEN NEW.state='ready' BEGIN SELECT RAISE(FAIL,'synthetic state commit failure'); END"); e != nil {
		t.Fatal(e)
	}
	if rr := accountCall(s, token); rr.Code != 502 {
		t.Fatal(rr.Code)
	}
	a, e := s.store.account("account")
	if e != nil || a.State != "refreshing" || issuer.Load() != 1 {
		t.Fatal("storage fault destroyed nonrotating credential", a.State, e, issuer.Load())
	}
	if _, e = s.store.db.Exec("DROP TRIGGER fail_copilot_ready"); e != nil {
		t.Fatal(e)
	}
	if rr := accountCall(s, token); rr.Code != 502 || issuer.Load() != 1 {
		t.Fatal("restoration bypassed cooldown", rr.Code, issuer.Load())
	}
	expireAccountBackoff(t, s)
	if rr := accountCall(s, token); rr.Code != 200 || issuer.Load() != 2 {
		t.Fatal("nonrotating account could not recover without login", rr.Code, issuer.Load())
	}
}

func TestAccountProactiveDialFailureUsesStillValidTokenDuringBackoff(t *testing.T) {
	for _, kind := range []string{"chatgpt", "copilot"} {
		t.Run(kind, func(t *testing.T) {
			var issuer, inference, dials atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					issuer.Add(1)
					t.Error("issuer should not have received a request")
					w.WriteHeader(500)
					return
				}
				inference.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-old-access" {
					t.Error("did not use the still-valid access token")
				}
				io.WriteString(w, "ok")
			}))
			defer up.Close()
			s := testService(t)
			a := testAccount(t, s, kind, up, false)
			a.ExpiresAt = time.Now().Add(3 * time.Minute).Unix()
			a.credentials.ExpiresAt = a.ExpiresAt
			if e := s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
				t.Fatal(e)
			}
			token := configureAccount(t, s, accountRoute(up.URL))
			dial := s.transport.DialContext
			s.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if dials.Add(1) == 1 {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				return dial(ctx, network, address)
			}
			// The first proactive refresh fails before connection acquisition; both that
			// request and the second one inside RetryAfter can use the unexpired token.
			for range 2 {
				if rr := accountCall(s, token); rr.Code != 200 {
					t.Fatal("valid token blocked by proactive retry policy", rr.Code, rr.Body.String())
				}
			}
			a, e := s.store.account("account")
			if e != nil || a.State != "ready" || a.refresh.RetryAfter <= time.Now().UnixMilli() || a.Generation != 1 || issuer.Load() != 0 || inference.Load() != 2 {
				t.Fatal("proactive fallback evidence", a.AccountInfo, e, issuer.Load(), inference.Load())
			}
		})
	}
}

func TestAccountProactiveRefreshIntervalStillAllowsUnexpiredToken(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/responses" {
			t.Error("refresh interval did not suppress issuer")
		}
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "chatgpt", up, false)
	a.ExpiresAt = time.Now().Add(3 * time.Minute).Unix()
	a.credentials.ExpiresAt = a.ExpiresAt
	a.refresh.LastSuccess = time.Now().UnixMilli()
	if e := s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
		t.Fatal(e)
	}
	token := configureAccount(t, s, accountRoute(up.URL))
	if rr := accountCall(s, token); rr.Code != 200 || calls.Load() != 1 {
		t.Fatal("refresh interval blocked valid token", rr.Code, calls.Load())
	}
}

func TestAccountRejectedTokenNeverFallsBackDuringSafeDialBackoff(t *testing.T) {
	var inference, dials atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inference.Add(1)
		w.WriteHeader(401)
		io.WriteString(w, "rejected")
	}))
	defer up.Close()
	s := testService(t)
	a := testAccount(t, s, "chatgpt", up, false)
	a.ExpiresAt = time.Now().Add(3 * time.Minute).Unix()
	a.credentials.ExpiresAt = a.ExpiresAt
	a.refresh.LastSuccess = time.Now().Add(-accountRefreshInterval - time.Second).UnixMilli()
	if e := s.store.replaceAccount(context.Background(), a, a.Generation); e != nil {
		t.Fatal(e)
	}
	token := configureAccount(t, s, accountRoute(up.URL))
	dial := s.transport.DialContext
	s.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if dials.Add(1) == 1 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		}
		return dial(ctx, network, address)
	}
	if rr := accountCall(s, token); rr.Code != 401 {
		t.Fatal("original 401 not passed through", rr.Code)
	}
	if rr := accountCall(s, token); rr.Code != 502 || inference.Load() != 1 {
		t.Fatal("known rejected token reused during backoff", rr.Code, inference.Load())
	}
	opts := s.opts
	s.Close()
	next, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	next.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if rr := accountCall(next, token); rr.Code != 502 || inference.Load() != 1 {
		t.Fatal("restart lost rejected generation", rr.Code, inference.Load())
	}
}
