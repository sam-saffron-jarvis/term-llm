package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

func TestAccountSchema2MigrationPreservesGrantsAndHistoryRollback(t *testing.T) {
	if err := sqliteutil.ValidateMigrations(migrations, 2, schemaVersion, true); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	r := Route{Name: "legacy", LocalPrefix: "/legacy/", Upstream: "https://api.example.test/base/", Enabled: true}
	configure(t, s, Config{Routes: []Route{r}}, nil)
	token := caller(t, s, r.Name)
	rec := Record{ID: "legacy-row", CallerID: "legacy-caller", Route: r.Name, Path: "/old", Method: "POST", Started: 1, Outcome: "unknown"}
	if err := s.store.start(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.store.db.QueryRow("SELECT data FROM requests WHERE id=?", rec.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Faithful schema2 shape: all pre-account tables/indexes are unchanged.
	if _, err := s.store.db.Exec("DROP TABLE connect_requests; DROP TABLE connect_managed; DROP TABLE accounts; DROP TABLE money_history; DROP TABLE money_settings; PRAGMA user_version=2;"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic failure after schema and marker writes")
	err := sqliteutil.WithImmediateMigrationTx(context.Background(), s.store.db, func(tx sqliteutil.Executor) error {
		if e := migrations[1].Up(tx); e != nil {
			return e
		}
		if _, e := tx.Exec("PRAGMA user_version=3"); e != nil {
			return e
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var marker, tables int
	s.store.db.QueryRow("PRAGMA user_version").Scan(&marker)
	s.store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='accounts'").Scan(&tables)
	if marker != 2 || tables != 0 {
		t.Fatal("account migration did not roll back", marker, tables)
	}
	if err = s.store.initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal("schema2 exact grant invalidated", err)
	}
	var after string
	s.store.db.QueryRow("SELECT data FROM requests WHERE id=?", rec.ID).Scan(&after)
	if before != after {
		t.Fatal("history JSON changed on migration")
	}
	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err = fresh.Exec(schema); err != nil {
		t.Fatal(err)
	}
	got, err := sqliteutil.SchemaSignature(context.Background(), s.store.db)
	if err != nil {
		t.Fatal(err)
	}
	want, err := sqliteutil.SchemaSignature(context.Background(), fresh)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("fresh/migrated schema mismatch", err)
	}
	opts := s.opts
	s.Close()
	restarted, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = restarted.store.authorize(token, restarted.snap.routes[0]); err != nil {
		t.Fatal("migrated grant lost on restart", err)
	}
}

func TestAccountFULLCustodyWritesAndRollback(t *testing.T) {
	s := testService(t)
	err := s.store.accountWrite(context.Background(), func(tx *sql.Tx) error {
		var value int
		if e := tx.QueryRow("PRAGMA synchronous").Scan(&value); e != nil {
			return e
		}
		if value != 2 {
			t.Fatalf("custody transaction is not synchronous FULL: %d", value)
		}
		_, e := tx.Exec("INSERT INTO accounts VALUES('rolled-back','copilot','ready',1,0,0,'{}')")
		if e != nil {
			return e
		}
		return errors.New("synthetic rollback")
	})
	if err == nil {
		t.Fatal("failed custody commit reported success")
	}
	var count, value int
	s.store.db.QueryRow("SELECT count(*) FROM accounts").Scan(&count)
	s.store.db.QueryRow("PRAGMA synchronous").Scan(&value)
	if count != 0 || value != 1 {
		t.Fatal("custody rollback/durability isolation", count, value)
	}
}

func TestAccountOperatorBootstrapOnceAndCrashFenceRestart(t *testing.T) {
	var issuer atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer.Add(1)
		io.WriteString(w, `{"access_token":"synthetic-rotated-access","refresh_token":"synthetic-rotated-refresh","expires_in":3600}`)
	}))
	defer up.Close()
	s := testService(t)
	opts := s.opts
	s.Close()
	dir := t.TempDir()
	private := filepath.Join(dir, "account.json")
	operator := filepath.Join(dir, "operator.yaml")
	data, _ := json.Marshal(map[string]any{"access_token": "synthetic-imported-access", "refresh_token": "synthetic-imported-refresh", "account_id": "synthetic-identity", "expires_at": time.Now().Add(-time.Hour).Unix(), "issuer": up.URL + "/oauth/token", "origins": []string{up.URL}})
	if err := os.WriteFile(private, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operator, []byte("routes: []\naccounts:\n  home:\n    kind: chatgpt\n    file: "+private+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts.ConfigFile = operator
	first, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	first.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	a, err := first.ensureAccount(context.Background(), "home", 0)
	if err != nil || a.Generation != 2 {
		t.Fatal("bootstrap refresh", err, a.Generation)
	}
	cookie, csrf := loginAdmin(t, first)
	rr := adminRequest(first, "GET", "export", nil, cookie, csrf)
	if strings.Contains(rr.Body.String(), private) || strings.Contains(rr.Body.String(), "accounts:") {
		t.Fatal("export leaked host-only bootstrap reference")
	}
	first.Close()
	next, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	a, err = next.store.account("home")
	if err != nil || a.Generation != 2 || a.credentials.RefreshToken != "synthetic-rotated-refresh" {
		t.Fatal("restart reimported stale bootstrap", err, a.Generation)
	}
	// Model a crash after committing the attempt, before receiving/persisting a
	// result. The source file still contains the old token: it must not be reused.
	if err = next.store.fenceAccount(context.Background(), "home", 2); err != nil {
		t.Fatal(err)
	}
	next.Close()
	recovered, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	a, err = recovered.store.account("home")
	if err != nil || a.State != "needs_login" {
		t.Fatal("fence recovery", a.State, err)
	}
	if _, err = recovered.ensureAccount(context.Background(), "home", 0); err == nil || issuer.Load() != 1 {
		t.Fatal("uncertain refresh automatically reused", err, issuer.Load())
	}
}

func TestAccountBootstrapPermissionsAndUIImport(t *testing.T) {
	s := testService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "public.json")
	data := `{"github_token":"synthetic-github-token"}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	s.opts.ConfigFile = "operator.yaml"
	c := Config{Accounts: map[string]AccountRef{"home": {Kind: "copilot", File: path}}}
	if err := s.bootstrapAccounts(context.Background(), c); err == nil {
		t.Fatal("public account bootstrap accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	s.opts.ConfigFile = ""
	if err := s.bootstrapAccounts(context.Background(), c); err == nil {
		t.Fatal("UI mode imported private operator file")
	}
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "POST", "import", map[string]any{"revision": s.snap.revision, "yaml": "routes: []\naccounts:\n  home:\n    kind: copilot\n    file: " + path}, cookie, csrf)
	if rr.Code != 400 {
		t.Fatal("admin import accepted bootstrap reference", rr.Code, rr.Body.String())
	}
	infos, err := s.store.accountInfos()
	if err != nil || len(infos) != 0 {
		t.Fatal("rejected bootstrap persisted account", err, len(infos))
	}
}
