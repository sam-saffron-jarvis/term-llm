package proxy

import (
	"context"
	"fmt"
	"github.com/term-llm/term-llm/internal/fileprivacy"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersistenceSessionsAndIndependentAuthority(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "PUT", "secrets", map[string]string{"name": "key", "origin": "https://example.test", "value": "upstream-private"}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	r := route("saved", "/saved/", "https://example.test/base/", "X-Local", "key")
	rr = adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{r}}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	_, token, err := s.store.createCaller("persisted", []string{"saved"}, s.snap)
	if err != nil {
		t.Fatal(err)
	}
	opts := s.opts
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if len(next.snap.routes) != 1 || next.snap.secrets["key"].Value != "upstream-private" {
		t.Fatal("route/secret did not survive restart")
	}
	if _, err := next.store.authorize(token, next.snap.routes[0]); err != nil {
		t.Fatal("caller/grants did not survive restart", err)
	}
	rr = adminRequest(next, "GET", "state", nil, cookie, csrf)
	if rr.Code != 401 {
		t.Fatal("admin session survived restart")
	}
	var journal string
	var sync int
	next.store.db.QueryRow("PRAGMA journal_mode").Scan(&journal)
	next.store.db.QueryRow("PRAGMA synchronous").Scan(&sync)
	if journal != "wal" || sync != 1 {
		t.Fatalf("durability %s %d", journal, sync)
	}
	paths, _ := filepath.Glob(filepath.Join(opts.StateDir, "proxy.db*"))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || fileprivacy.CheckFile(info) != nil {
			t.Fatalf("insecure state %s %v", path, err)
		}
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(operator, []byte("routes: []\n"), 0600)
	opts.ConfigFile = operator
	fileMode, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fileMode.Close()
	if len(fileMode.snap.routes) != 0 {
		t.Fatal("file mode merged old UI routes")
	}
	fileMode.Close()
	os.WriteFile(operator, []byte("not_a_field: true\n"), 0600)
	if bad, err := Open(opts); err == nil {
		bad.Close()
		t.Fatal("invalid file fell back to UI config")
	}
}
func TestTerminalFailureRemainsUnknownWithoutLatching(t *testing.T) {
	var dispatched atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dispatched.Add(1); w.Write([]byte("ok")) }))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "secret")})
	token := caller(t, s, "r")
	_, err := s.store.db.Exec("CREATE TRIGGER fail_terminal BEFORE UPDATE ON requests BEGIN SELECT RAISE(FAIL,'synthetic storage fault'); END")
	if err != nil {
		t.Fatal(err)
	}
	if rr := call(s, "GET", "/r/path", "X-Local", token, nil); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	records, _, err := s.store.records(nil, 10)
	if err != nil || len(records) != 1 || records[0].Ended != 0 || records[0].Outcome != "unknown" {
		t.Fatalf("fabricated terminal outcome %+v %v", records, err)
	}
	if _, err = s.store.db.Exec("DROP TRIGGER fail_terminal"); err != nil {
		t.Fatal(err)
	}
	if rr := call(s, "GET", "/r/path", "X-Local", token, nil); rr.Code != 200 || dispatched.Load() != 2 {
		t.Fatal("transient terminal failure latched service", rr.Code, dispatched.Load())
	}
}
func TestMetadataRedactionAndBoundedExport(t *testing.T) {
	s := testService(t)
	value := "a-known-private-value"
	snap := &snapshot{secrets: map[string]material{"key": {Origin: "https://example.test:443", Value: value}}}
	path := strings.Repeat("x", 2040) + value
	if redacted := s.redactPath(path, "local-caller", snap); strings.Contains(redacted, "a-known") || len(redacted) > 2048 {
		t.Fatal("truncation leaked known secret", redacted)
	}
	if redacted := s.redactPath("%61-known-private-value/local-caller", "local-caller", snap); strings.Contains(redacted, value) || strings.Contains(redacted, "local-caller") {
		t.Fatal("encoded secret leaked", redacted)
	}
	for i := 0; i < 1002; i++ {
		r := Record{ID: randomToken("r_"), Started: int64(i), Route: "route", CallerID: "caller", Outcome: "unknown"}
		if err := s.store.start(context.Background(), &r); err != nil {
			t.Fatal(err)
		}
	}
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "GET", "requests/export", nil, cookie, csrf)
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("export failed")
	}
	if strings.Count(rr.Body.String(), `"caller_id"`) != 1000 {
		t.Fatal("export not bounded")
	}
}
func TestConfigDefaultsAndFutureSchema(t *testing.T) {
	c, err := decodeConfig([]byte("routes:\n  - name: plain\n    local_prefix: /plain/\n    upstream: https://example.test/\n"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := compile(c, 1, nil)
	if err != nil || !snap.routes[0].Enabled || snap.routes[0].CallerAuth.Prefix != "Bearer " {
		t.Fatalf("defaults %+v %v", snap, err)
	}
	if !safePath("/id%25remaining") {
		t.Fatal("literal escaped percent rejected")
	}
	s := testService(t)
	if _, err := s.store.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := s.store.initialize(); err == nil {
		t.Fatal("future schema accepted")
	}
}

func TestActiveAndUnknownFilters(t *testing.T) {
	s := testService(t)
	for _, id := range []string{"active-request", "old-unknown"} {
		r := Record{ID: id, Outcome: "unknown"}
		if err := s.store.start(context.Background(), &r); err != nil {
			t.Fatal(err)
		}
	}
	s.active.Store("active-request", true)
	defer s.active.Delete("active-request")
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "GET", "requests?outcome=active", nil, cookie, csrf)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "active-request") || strings.Contains(rr.Body.String(), "old-unknown") {
		t.Fatal("active filter", rr.Body.String())
	}
	rr = adminRequest(s, "GET", "requests?outcome=unknown&_active=old-unknown", nil, cookie, csrf)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "active-request") || !strings.Contains(rr.Body.String(), "old-unknown") {
		t.Fatal("unknown filter", rr.Body.String())
	}
}

func TestStoredReferencesHaveStableNames(t *testing.T) {
	c := Config{Secrets: map[string]SecretRef{"alias": {Stored: "other"}}}
	if _, err := resolveSecrets(c, map[string]material{"other": {Origin: "https://example.test:443", Value: "private"}}, "", false); err == nil {
		t.Fatal("mutable alias accepted")
	}
}

func TestAdminSessionExpiryAndThrottle(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	s.authMu.Lock()
	key := hashToken(cookie.Value)
	expired := s.sessions[key]
	expired.expires = expired.expires.Add(-9 * time.Hour)
	s.sessions[key] = expired
	s.authMu.Unlock()
	if rr := adminRequest(s, "GET", "state", nil, cookie, csrf); rr.Code != 401 {
		t.Fatal("expired session accepted")
	}
	for i := 0; i < 9; i++ {
		rr := adminRequest(s, "POST", "login", map[string]string{"token": "invalid"}, nil, "")
		if i == 8 && rr.Code != 429 {
			t.Fatalf("login not throttled %d", rr.Code)
		}
	}
	// The exact same peer's exhausted bad-attempt bucket must not block a valid token.
	loginAdmin(t, s)
	s.opts.PublicURL = "https://proxy.example.test"
	req := httptest.NewRequest("POST", "https://proxy.example.test/_proxy/api/login", strings.NewReader(`{"token":"`+strings.Repeat("a", 64)+`"}`))
	req.Header.Set("Origin", "https://proxy.example.test")
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.5:1234"
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 || len(rr.Result().Cookies()) != 1 || !rr.Result().Cookies()[0].Secure {
		t.Fatalf("HTTPS admin cookie %d %s", rr.Code, rr.Body.String())
	}
}
