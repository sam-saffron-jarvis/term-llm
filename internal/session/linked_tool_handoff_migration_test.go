package session

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

func TestLinkedToolHandoffMigrationV71(t *testing.T) {
	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := initSchema(fresh); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err := initSchema(legacy); err != nil {
		t.Fatal(err)
	}
	// Reconstruct v70: the only v71 object is the new handoff table.
	if _, err := legacy.Exec(`DROP TABLE linked_tool_receipts; DROP TABLE linked_tool_handoffs; UPDATE schema_version SET version=70;
 INSERT INTO sessions(id,provider,model) VALUES('legacy','debug','fast');
 INSERT INTO messages(session_id,role,parts,sequence,response_id) VALUES('legacy','assistant','[]',0,'legacy-run');`); err != nil {
		t.Fatal(err)
	}
	original := migrations
	defer func() { migrations = original }()
	migrations = append([]migration(nil), original...)
	last := &migrations[len(migrations)-2]
	if last.version != 71 {
		t.Fatalf("expected to inject failure into v71, got v%d", last.version)
	}
	up := last.up
	last.up = func(db schemaExecutor) error {
		if err := up(db); err != nil {
			return err
		}
		return errors.New("injected handoff migration failure")
	}
	if err := initSchema(legacy); err == nil || !strings.Contains(err.Error(), "injected handoff migration failure") {
		t.Fatalf("unexpected migration failure: %v", err)
	}
	var version int
	if err := legacy.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != 70 {
		t.Fatalf("rollback marker: %d, %v", version, err)
	}
	if exists, err := sqliteutil.TableExists(legacy, "linked_tool_handoffs"); err != nil || exists {
		t.Fatalf("handoff table escaped rollback: %t, %v", exists, err)
	}
	migrations = original
	if err := initSchema(legacy); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := legacy.QueryRow(`SELECT COUNT(*) FROM messages WHERE response_id='legacy-run'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy message not preserved: %d, %v", count, err)
	}
	if err := legacy.QueryRow(`SELECT COUNT(*) FROM linked_tool_handoffs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy messages must not gain unproven authority: %d, %v", count, err)
	}
	want, err := sqliteutil.SchemaSignature(t.Context(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqliteutil.SchemaSignature(t.Context(), legacy)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh and v70-upgraded schemas differ: %v", err)
	}
}
