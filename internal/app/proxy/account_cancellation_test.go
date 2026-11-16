package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountCallerCancellationDoesNotAbortConnectedTrustedRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var issuer, inference atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			issuer.Add(1)
			close(entered)
			<-release
			io.WriteString(w, `{"access_token":"synthetic-late-access","refresh_token":"synthetic-late-refresh","expires_in":3600}`)
			return
		}
		inference.Add(1)
		io.WriteString(w, "must not dispatch canceled inference")
	}))
	defer up.Close()
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	s := testService(t)
	testAccount(t, s, "chatgpt", up, true)
	token := configureAccount(t, s, accountRoute(up.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "/account/responses", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(httptest.NewRecorder(), req) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("issuer never received refresh")
	}
	cancel()
	finish()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("trusted flight failed to finish")
	}
	a, err := s.store.account("account")
	if err != nil || a.State != "ready" || a.Generation != 2 || a.credentials.RefreshToken != "synthetic-late-refresh" || issuer.Load() != 1 || inference.Load() != 0 {
		t.Fatal("caller cancellation damaged custody or dispatched inference", a.AccountInfo, err, issuer.Load(), inference.Load())
	}
	if req.Context().Err() != context.Canceled {
		t.Fatal("inference context was replaced")
	}
}

func TestAccountCanceledCallerDoesNotCancelRejectedGenerationWrite(t *testing.T) {
	s := testService(t)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("rate-limited rejection must not contact issuer")
	}))
	defer up.Close()
	a := testAccount(t, s, "chatgpt", up, false)
	a.refresh.LastSuccess = time.Now().UnixMilli()
	if err := s.store.replaceAccount(context.Background(), a, a.Generation); err != nil {
		t.Fatal(err)
	}
	// Hold the sole DB connection to stop the acquired account flight before its
	// first read/write, then cancel its worker. No sleeps or issuer calls needed.
	conn, err := s.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	gate := make(chan struct{}, 1)
	s.accountLocks.Store("account", gate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := s.ensureAccount(ctx, "account", 1); done <- e }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for len(gate) == 0 {
		select {
		case <-deadline.C:
			t.Fatal("account flight never acquired lock")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	conn.Close()
	select {
	case err = <-done:
		if err != errAccountRefreshLimited {
			t.Fatal("unexpected canceled-latch result", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled latch write stuck")
	}
	a, err = s.store.account("account")
	if err != nil || a.State != "ready" || a.refresh.RejectedGeneration != 1 {
		t.Fatal("caller cancellation prevented trusted latch write", a.AccountInfo, err)
	}
}

func TestAccountRejectedLatchStorageFailureDoesNotSignOut(t *testing.T) {
	s := testService(t)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("latch failure must not contact issuer") }))
	defer up.Close()
	a := testAccount(t, s, "chatgpt", up, false)
	a.refresh.LastSuccess = time.Now().UnixMilli()
	if err := s.store.replaceAccount(context.Background(), a, a.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec("CREATE TRIGGER reject_latch_write BEFORE UPDATE ON accounts BEGIN SELECT RAISE(FAIL,'synthetic transient latch failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ensureAccount(context.Background(), "account", 1); err != errAccountRefreshLimited {
		t.Fatal("latch failure was not retry-limited", err)
	}
	a, err := s.store.account("account")
	if err != nil || a.State != "ready" || !s.accountGenerationRejected(a) {
		t.Fatal("latch failure signed out account or lost rejection", a.AccountInfo, err)
	}
	if _, err = s.store.db.Exec("DROP TRIGGER reject_latch_write"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureAccount(context.Background(), "account", 0); err != errAccountRefreshLimited {
		t.Fatal("failed durable latch allowed rejected-token fallback", err)
	}
	if _, err = s.ensureAccount(context.Background(), "account", 1); err != errAccountRefreshLimited {
		t.Fatal("latch persistence retry lost rate bound", err)
	}
	a, err = s.store.account("account")
	if err != nil || a.State != "ready" || a.refresh.RejectedGeneration != 1 {
		t.Fatal("rejection could not recover without login", a.AccountInfo, err)
	}
}
