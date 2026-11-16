package proxy

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

func TestConnectSchema3MigrationPreservesExistingAuthorityAndRollsBack(t *testing.T) {
	if err := sqliteutil.ValidateMigrations(migrations, 2, schemaVersion, true); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	account := accountRecord{AccountInfo: AccountInfo{Name: "legacy", Kind: "claude-code", State: "ready", Generation: 1, Updated: time.Now().UnixMilli()}, credentials: accountCredentials{Mode: "token_only", AccessToken: "synthetic-schema3-access"}}
	if err := s.store.replaceAccount(context.Background(), account, 0); err != nil {
		t.Fatal(err)
	}
	configure(t, s, Config{Routes: []Route{connectRoute("legacy")}}, nil)
	token := caller(t, s, "legacy")
	authority := s.snap.routes[0].authority
	rec := Record{ID: "schema3-history", Started: time.Now().UnixMilli(), Route: "legacy", Outcome: "unknown"}
	if err := s.store.start(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
	var before string
	s.store.db.QueryRow("SELECT data FROM requests WHERE id=?", rec.ID).Scan(&before)
	// Faithful schema3 fixture: every pre-connect table/index is unchanged.
	if _, err := s.store.db.Exec("DROP TABLE connect_requests; DROP TABLE connect_managed; PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("synthetic migration failure after connect DDL and marker")
	err := sqliteutil.WithImmediateMigrationTx(context.Background(), s.store.db, func(tx sqliteutil.Executor) error {
		if e := migrations[len(migrations)-1].Up(tx); e != nil {
			return e
		}
		if _, e := tx.Exec("PRAGMA user_version=4"); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var version, count int
	s.store.db.QueryRow("PRAGMA user_version").Scan(&version)
	s.store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='connect_requests'").Scan(&count)
	if version != 3 || count != 0 {
		t.Fatal("connect migration did not roll back", version, count)
	}
	if err = s.store.initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.authorize(token, s.snap.routes[0]); err != nil || s.snap.routes[0].authority != authority {
		t.Fatal("schema3 grants changed", err)
	}
	a, err := s.store.account("legacy")
	if err != nil || a.credentials.AccessToken != "synthetic-schema3-access" || a.Generation != 1 {
		t.Fatal("schema3 account changed", err)
	}
	var after string
	s.store.db.QueryRow("SELECT data FROM requests WHERE id=?", rec.ID).Scan(&after)
	if before != after {
		t.Fatal("schema3 history changed")
	}
	s.store.db.QueryRow("SELECT count(*) FROM connect_managed").Scan(&count)
	if count != 0 {
		t.Fatal("migration adopted unmanaged routes")
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
}

func TestConnectActualCommitFailureRollsBackSnapshotAndReceipt(t *testing.T) {
	s := testService(t)
	input := connectInput(301, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	revision := s.snap.revision
	_, err := s.store.db.Exec("PRAGMA foreign_keys=ON; CREATE TABLE connect_test_parent(id INTEGER PRIMARY KEY); CREATE TABLE connect_test_child(parent INTEGER REFERENCES connect_test_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_connect_commit AFTER UPDATE ON connect_requests WHEN NEW.status='complete' BEGIN INSERT INTO connect_test_child VALUES(123); END;")
	if err != nil {
		t.Fatal(err)
	}
	rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: "synthetic-rollback"})
	if rr.Code != 503 {
		t.Fatal("failed COMMIT reported success", rr.Code, rr.Body.String())
	}
	assertConnectCounts(t, s, 0, 0, 0, 0)
	var version int64
	s.store.db.QueryRow("SELECT revision FROM configuration WHERE id=1").Scan(&version)
	if version != revision || s.snap.revision != revision {
		t.Fatal("failed COMMIT changed runtime/config revision")
	}
	var status string
	s.store.db.QueryRow("SELECT status FROM connect_requests WHERE id=?", input.ID).Scan(&status)
	if status != "approved" {
		t.Fatal("failed COMMIT lost recoverable approval", status)
	}
	if _, err = s.store.db.Exec("DROP TRIGGER fail_connect_commit"); err != nil {
		t.Fatal(err)
	}
	commitConnectTest(t, s, input, "synthetic-rollback")
	assertConnectCounts(t, s, 1, 1, 1, 1)
}
