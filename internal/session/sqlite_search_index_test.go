package session

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

func TestSearchIndexV73Migration(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err := initSchema(legacy); err != nil {
		t.Fatal(err)
	}
	// Synthetic v72 fixture: the only v73 object is the search index.
	if _, err := legacy.Exec(`DROP INDEX idx_messages_search_visible; UPDATE schema_version SET version=72;
		INSERT INTO sessions(id,provider,model) VALUES('fixture','test','test');
		INSERT INTO messages(session_id,role,parts,sequence,text_content) VALUES
		 ('fixture','user','[]',0,'visible alpine'),('fixture','assistant','[]',1,'visible glacier'),
		 ('fixture','system','[]',2,'private sentinel'),('fixture','user','[]',3,'[Context Compaction] private summary');`); err != nil {
		t.Fatal(err)
	}
	read := func(omit bool, indexed bool) []SearchResult {
		t.Helper()
		s, err := NewSQLiteStore(Config{Enabled: true, Path: path, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if got := s.hasSearchMessageIndex; got != indexed {
			t.Fatalf("search index detected = %v, want %v", got, indexed)
		}
		r, err := s.Search(ctx, SearchOptions{Query: "alpine glacier", OmitSnippet: omit})
		if err != nil || len(r) != 1 || r[0].MessageID == 0 || (r[0].Snippet == "") != omit {
			t.Fatalf("search: %#v, %v", r, err)
		}
		return r
	}
	before := read(false, false)
	original := migrations
	defer func() { migrations = original }()
	migrations = append([]migration(nil), original...)
	last := &migrations[len(migrations)-1]
	if last.version != 73 {
		t.Fatalf("expected v73, got v%d", last.version)
	}
	up := last.up
	last.up = func(db schemaExecutor) error {
		if err := up(db); err != nil {
			return err
		}
		return errors.New("injected search index failure")
	}
	if err := initSchema(legacy); err == nil || !strings.Contains(err.Error(), "injected search index failure") {
		t.Fatalf("migration failure: %v", err)
	}
	var n int
	if err := legacy.QueryRow(`SELECT version FROM schema_version`).Scan(&n); err != nil || n != 72 {
		t.Fatalf("rollback marker: %d, %v", n, err)
	}
	read(false, false) // the index also rolled back
	migrations = original
	if err := initSchema(legacy); err != nil {
		t.Fatal(err)
	}
	if after := read(false, true); !reflect.DeepEqual(before, after) {
		t.Fatalf("search changed after upgrade: %#v vs %#v", before, after)
	}
	before[0].Snippet = ""
	if after := read(true, true); !reflect.DeepEqual(before, after) {
		t.Fatalf("omitting snippets changed other data: %#v vs %#v", before, after)
	}
	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := initSchema(fresh); err != nil {
		t.Fatal(err)
	}
	want, err := sqliteutil.SchemaSignature(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sqliteutil.SchemaSignature(ctx, legacy); err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("fresh and v72-upgraded schemas differ: %v", err)
	}
}
