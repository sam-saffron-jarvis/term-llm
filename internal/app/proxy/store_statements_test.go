package proxy

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPreparedAuthorizationObservesCurrentAuthority(t *testing.T) {
	s := testService(t)
	r := route("r", "/r/", "https://example.test/base/", "X-Local", "key")
	materials := map[string]material{"key": boundMaterial(t, r.Upstream, "real")}
	configure(t, s, Config{Routes: []Route{r}}, materials)
	c, token, err := s.store.createCaller("prepared caller", []string{"r"}, s.snap)
	if err != nil {
		t.Fatal(err)
	}
	original := s.snap.routes[0]
	check := func(stage string, active activeRoute, allowed bool) {
		t.Helper()
		// Exercise the same statement repeatedly on both hits and misses.
		for range 3 {
			got, err := s.store.authorize(token, active)
			if allowed {
				if err != nil || got.ID != c.ID || got.Name != c.Name {
					t.Fatalf("%s: caller %+v, error %v", stage, got, err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s: wanted denied authorization, got %+v, %v", stage, got, err)
			}
		}
	}
	check("initial grant", original, true)
	if err := s.store.updateGrants(c.ID, nil, s.snap); err != nil {
		t.Fatal(err)
	}
	check("removed grant", original, false)
	if err := s.store.updateGrants(c.ID, []string{"r"}, s.snap); err != nil {
		t.Fatal(err)
	}
	check("explicit regrant", original, true)

	r.Upstream = "https://example.test/expanded/"
	configure(t, s, Config{Routes: []Route{r}}, materials)
	current := s.snap.routes[0]
	check("changed route authority", current, false)
	check("stale route authority", original, false)
	if err := s.store.updateGrants(c.ID, []string{"r"}, s.snap); err != nil {
		t.Fatal(err)
	}
	check("regrant changed authority", current, true)
	check("old authority after regrant", original, false)

	// A prepared authorization must also survive the same live schema changes
	// used by migration recovery, without retaining the invalidated grant.
	if _, err := s.store.db.Exec(removeProxyV7 + removeProxyV6 + "DROP TABLE accounts; DROP TABLE money_history; DROP TABLE money_settings; ALTER TABLE grants RENAME COLUMN authority TO origin; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.initialize(); err != nil {
		t.Fatal(err)
	}
	check("migration invalidated grant", current, false)
	if err := s.store.updateGrants(c.ID, []string{"r"}, s.snap); err != nil {
		t.Fatal(err)
	}
	check("regrant after migration", current, true)
	if _, err := s.store.db.Exec("UPDATE callers SET revoked=1 WHERE id=?", c.ID); err != nil {
		t.Fatal(err)
	}
	check("revoked caller", current, false)
}

func TestPreparedAuditStatementsPersistRecordsAndSequence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.close() })
	first := Record{ID: "first", Started: 1001, CallerID: "caller", Route: "r", Method: "POST", Path: "/one", Outcome: "unknown"}
	second := Record{ID: "second", Started: 1002, CallerID: "caller", Route: "r", Method: "GET", Path: "/two", Outcome: "unknown"}
	for _, r := range []*Record{&first, &second} {
		if err := s.start(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if first.Seq <= 0 || second.Seq <= first.Seq {
		t.Fatalf("non-monotonic admission sequences: %d, %d", first.Seq, second.Seq)
	}
	duplicate := first
	if err := s.start(context.Background(), &duplicate); err == nil {
		t.Fatal("duplicate request ID bypassed the database constraint")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := Record{ID: "canceled", Outcome: "unknown"}
	if err := s.start(ctx, &canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("start ignored caller cancellation: %v", err)
	}
	if canceled.Seq != 0 {
		t.Fatalf("canceled admission acquired sequence %d", canceled.Seq)
	}
	first.Ended, first.Status, first.Outcome = 1010, 201, "ok"
	first.Sent, first.Received = 20, 30
	if err := s.finish(first); err != nil {
		t.Fatal(err)
	}
	check := func(st *store) {
		t.Helper()
		records, next, err := st.records(nil, 10)
		if err != nil || next != 0 || !reflect.DeepEqual(records, []Record{second, first}) {
			t.Fatalf("records %+v, next %d, error %v", records, next, err)
		}
		// The terminal update must persist both its JSON and indexed outcome.
		finished, _, err := st.records(url.Values{"outcome": {"ok"}}, 10)
		if err != nil || !reflect.DeepEqual(finished, []Record{first}) {
			t.Fatalf("terminal outcome not persisted: %+v, %v", finished, err)
		}
	}
	check(s)
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.close() })
	check(reopened)
	third := Record{ID: "third", Path: "/three", Outcome: "unknown"}
	if err := reopened.start(context.Background(), &third); err != nil || third.Seq <= second.Seq {
		t.Fatalf("sequence not preserved after reopen: %+v, %v", third, err)
	}
}

func TestStoreStatementCleanup(t *testing.T) {
	for _, tt := range []struct {
		name, change, failure string
	}{
		{"fully prepared", "", ""},
		{"money history failure", "DROP TABLE money_history", "money history"},
		{"authorization failure", "DROP TABLE grants", "authorization"},
		{"start failure", "DROP TABLE requests", "request start"},
		{"finish failure", `DROP TABLE requests;
CREATE VIEW requests AS SELECT '' AS id, 0 AS started, '' AS caller, '' AS route, '' AS method, '' AS path, '' AS outcome, '' AS data;
CREATE TRIGGER allow_start INSTEAD OF INSERT ON requests BEGIN SELECT 1; END;`, "request finish"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			s := &store{db: db}
			t.Cleanup(func() { _ = s.close() })
			if _, err := db.Exec(schema); err != nil {
				t.Fatal(err)
			}
			if tt.change != "" {
				if _, err := db.Exec(tt.change); err != nil {
					t.Fatal(err)
				}
			}
			err = s.prepareStatements()
			if tt.failure != "" {
				if err == nil || !strings.Contains(err.Error(), "prepare proxy "+tt.failure+" statement") {
					t.Fatalf("missing contextual prepare failure: %v", err)
				}
				// Check before another close: failed preparation itself must release
				// previously prepared statements and the database connection.
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := s.close(); err != nil {
					t.Fatal(err)
				}
			}
			assertStoreResourcesClosed(t, s)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					if err := s.close(); err != nil {
						t.Errorf("repeated close: %v", err)
					}
				})
			}
			wg.Wait()
		})
	}
}

func assertStoreResourcesClosed(t *testing.T, s *store) {
	t.Helper()
	if err := s.db.Ping(); err == nil {
		t.Fatal("closed database is still usable")
	}
	if n := s.db.Stats().OpenConnections; n != 0 {
		t.Fatalf("closed database retained %d connections", n)
	}
	for name, stmt := range map[string]*sql.Stmt{"authorization": s.authorizeStmt, "stream authorization": s.streamGrantStmt, "settings": s.settingsStmt, "start": s.startStmt, "finish": s.finishStmt, "money history": s.moneyStmt} {
		if stmt == nil {
			continue
		}
		if _, err := stmt.Exec(); err == nil || !strings.Contains(err.Error(), "statement is closed") {
			t.Errorf("%s statement usable after close: %v", name, err)
		}
	}
}

func TestOpenStoreReportsPrepareFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.close() })
	if _, err := s.db.Exec("DROP TABLE requests"); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	failed, err := openStore(dir)
	if failed != nil {
		_ = failed.close()
		t.Fatal("opened store with unusable audit schema")
	}
	if err == nil || !strings.Contains(err.Error(), "prepare proxy request start statement") {
		t.Fatalf("missing startup prepare context: %v", err)
	}
}

// A reader must not queue behind a write or observe its uncommitted state. The
// next read must see the committed revocation/price, not a cached decision.
func TestProxyReadersFollowCommittedWAL(t *testing.T) {
	s := testService(t)
	r := route("r", "/r/", "https://example.test/base/", "X-Local", "key")
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "real")})
	caller, token, err := s.store.createCaller("wal", []string{"r"}, s.snap)
	if err != nil {
		t.Fatal(err)
	}
	st := s.store
	if _, err := st.readDB.Exec("UPDATE callers SET revoked=1 WHERE 0"); err == nil {
		t.Fatal("read-only pool accepted a write")
	}
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE callers SET revoked=1 WHERE id=?", caller.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE money_settings SET data='{"timezone":"UTC"}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	// A deadline makes an accidental read on the occupied writer fail, not hang.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if settings, err := st.loadMoneySettings(ctx); err != nil || settings.Timezone == "UTC" {
		t.Fatalf("uncommitted price view %+v, %v", settings, err)
	}
	if _, err = st.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if settings, err := st.loadMoneySettings(ctx); err != nil || settings.Timezone != "UTC" {
		t.Fatalf("stale price view %+v, %v", settings, err)
	}
	if _, err = st.authorize(token, s.snap.routes[0]); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked grant: %v", err)
	}
}
