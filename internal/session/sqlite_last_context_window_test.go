package session

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

func lastContextWindowV69Fixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE sessions DROP COLUMN last_context_window; UPDATE schema_version SET version=69;
 INSERT INTO sessions(id,provider,model,created_at,updated_at) VALUES('legacy','mock','mock',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
}

func TestLastContextWindowMigrationV70(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	lastContextWindowV69Fixture(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	var v, w int
	if err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("version=%d err=%v", v, err)
	}
	if err := db.QueryRow(`SELECT last_context_window FROM sessions WHERE id='legacy'`).Scan(&w); err != nil || w != 0 {
		t.Fatalf("window=%d err=%v", w, err)
	}
	// Idempotent.
	if err := migrateLastContextWindowV70(db); err != nil {
		t.Fatal(err)
	}
	ok, err := sqliteutil.ColumnExists(db, "sessions", "last_context_window")
	if err != nil || !ok {
		t.Fatalf("column exists=%v err=%v", ok, err)
	}
}

func TestLastContextWindowFreshSchemaAndV69ReadOnly(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "fresh.db")
	store, err := NewSQLiteStore(Config{Path: fresh})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := sqliteutil.ColumnExists(store.db, "sessions", "last_context_window")
	if err != nil || !ok {
		t.Fatalf("fresh column exists=%v err=%v", ok, err)
	}
	_ = store.Close()

	old := filepath.Join(t.TempDir(), "old.db")
	lastContextWindowV69Fixture(t, old)
	ro, err := NewSQLiteStore(Config{Path: old, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if ro.hasLastContextWindow {
		t.Fatal("read-only v69 store must not report last_context_window")
	}
	got, err := ro.Get(context.Background(), "legacy")
	if err != nil || got == nil || got.ID != "legacy" {
		t.Fatalf("read-only Get on v69 db: %+v err=%v", got, err)
	}
	var v int
	if err := ro.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil || v != 69 {
		t.Fatalf("read-only open changed version=%d err=%v", v, err)
	}
}

func columnInfo(t *testing.T, db *sql.DB) (typ string, notNull int, dflt sql.NullString) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, pk int
		var name, ty string
		var nn int
		var d sql.NullString
		if err := rows.Scan(&cid, &name, &ty, &nn, &d, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "last_context_window" {
			return ty, nn, d
		}
	}
	t.Fatal("last_context_window column missing")
	return
}

func TestLastContextWindowFreshAndMigratedSchemaEquivalent(t *testing.T) {
	open := func(path string) *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	fresh := open(filepath.Join(t.TempDir(), "fresh.db"))
	if err := initSchema(fresh); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old.db")
	lastContextWindowV69Fixture(t, oldPath)
	migrated := open(oldPath)
	if err := initSchema(migrated); err != nil {
		t.Fatal(err)
	}
	ft, fn, fd := columnInfo(t, fresh)
	mt, mn, md := columnInfo(t, migrated)
	if ft != mt || fn != mn || fd != md {
		t.Fatalf("fresh (%s,%d,%v) != migrated (%s,%d,%v)", ft, fn, fd, mt, mn, md)
	}
	if ft != "INTEGER" || fn != 1 || !fd.Valid || fd.String != "0" {
		t.Fatalf("unexpected definition (%s,%d,%v)", ft, fn, fd)
	}
	// Default applies to inserts that omit the column.
	var w int
	if _, err := fresh.Exec(`INSERT INTO sessions(id,provider,model,created_at,updated_at) VALUES('d','mock','mock',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := fresh.QueryRow(`SELECT last_context_window FROM sessions WHERE id='d'`).Scan(&w); err != nil || w != 0 {
		t.Fatalf("default window=%d err=%v", w, err)
	}
	// NOT NULL is enforced.
	if _, err := fresh.Exec(`UPDATE sessions SET last_context_window=NULL WHERE id='d'`); err == nil {
		t.Fatal("expected NOT NULL violation")
	}
}

func TestLastContextWindowMigrationErrorPaths(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	// No sessions table: ALTER must fail with wrapped context.
	if err := migrateLastContextWindowV70(db); err == nil {
		t.Fatal("expected error without sessions table")
	}
	// Closed database surfaces the inspection error.
	_ = db.Close()
	if err := migrateLastContextWindowV70(db); err == nil {
		t.Fatal("expected error on closed db")
	}
}
