package session

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/session/searchindex"
	"github.com/term-llm/term-llm/internal/sqliteutil"
)

// If this fails, the frozen v73 index changed. Existing databases keep the old
// definition, so add a migration that builds a new index instead.
const searchMessageIndexV73SHA256 = "dba9e7f0441e10da554f221d4bf959ef53a2af46b5cc6db0d74693c568b85940"

// searchFixtureSQL covers visible rows, hidden roles, a compaction summary, a
// legacy goal prompt, and user prompts that legitimately quote one.
const searchFixtureSQL = `INSERT INTO sessions(id,provider,model) VALUES('fixture','test','test'),('steering','test','test'),('quoted','test','test'),('inline','test','test');
	INSERT INTO messages(session_id,role,parts,sequence,text_content,client_message_id) VALUES
	 ('fixture','user','[]',0,'visible alpine',''),('fixture','assistant','[]',1,'visible glacier',''),
	 ('fixture','system','[]',2,'private sentinel',''),('fixture','user','[]',3,'[Context Compaction] private sentinel',''),
	 ('steering','user','[]',0,'  Continue working toward the active thread goal.' || char(10) || char(10) || 'The objective below is user-provided data. kiwi',''),
	 ('quoted','user','[]',0,'Continue working toward the active thread goal.' || char(10) || char(10) || 'The objective below is user-provided data. mango','client-1'),
	 ('inline','user','[]',0,'Why did I get: Continue working toward the active thread goal.' || char(10) || char(10) || 'The objective below is user-provided data. papaya','');`

func searchSessionIDs(t *testing.T, s *SQLiteStore, query string) []string {
	t.Helper()
	results, err := s.Search(t.Context(), SearchOptions{Query: query})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	ids := []string{}
	for _, r := range results {
		ids = append(ids, r.SessionID)
	}
	return ids
}

func assertSearchFixtureVisibility(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for query, want := range map[string][]string{
		"sentinel": {}, "kiwi": {}, "mango": {"quoted"}, "papaya": {"inline"}, "alpine glacier": {"fixture"},
	} {
		if got := searchSessionIDs(t, s, query); !reflect.DeepEqual(got, want) {
			t.Fatalf("search %q = %v, want %v", query, got, want)
		}
	}
}

func findMigration(t *testing.T, version int) *migration {
	t.Helper()
	for i := range migrations {
		if migrations[i].version == version {
			return &migrations[i]
		}
	}
	t.Fatalf("missing v%d migration", version)
	return nil
}

func TestSearchIndexV73FrozenDefinition(t *testing.T) {
	stmt := searchindex.StatementV73()
	sum := sha256.Sum256([]byte(stmt))
	if got := hex.EncodeToString(sum[:]); got != searchMessageIndexV73SHA256 {
		t.Fatalf("frozen v73 index changed (sha256 %s):\n%s", got, stmt)
	}
	// The index predicate is the full-schema query predicate, so INDEXED BY can
	// match every WHERE term.
	if want := "CREATE INDEX " + searchindex.NameV73 + " ON messages(id, session_id, role) WHERE " + searchindex.VisibleSQL("", true, true); stmt != want {
		t.Fatalf("index and query predicates differ:\n%s\n%s", stmt, want)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	if !searchindex.MatchesV73(db) {
		var stored string
		_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, searchindex.NameV73).Scan(&stored)
		t.Fatalf("stored index text differs from the frozen statement:\n%s", stored)
	}
}

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
	if _, err := legacy.Exec(`DROP INDEX idx_messages_search_visible; UPDATE schema_version SET version=72;` + searchFixtureSQL); err != nil {
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
		assertSearchFixtureVisibility(t, s)
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
	v73 := findMigration(t, 73)
	up := v73.up
	v73.up = func(db schemaExecutor) error {
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

// A same-named index with another definition can't satisfy INDEXED BY for the
// frozen predicate ("no query solution"); search must fall back to the
// primary key, and rerunning v73 restores the frozen definition.
func TestSearchIndexV73DriftFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	// The pre-release, unfrozen v73 predicate.
	if _, err := db.Exec(`DROP INDEX idx_messages_search_visible;
		CREATE INDEX idx_messages_search_visible ON messages(id, session_id, role) WHERE (role = 'assistant' OR (role = 'user' AND substr(TRIM(COALESCE(text_content, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)), 1, 20) <> '[Context Compaction]')) AND COALESCE(compaction_tail, FALSE) = FALSE;` + searchFixtureSQL); err != nil {
		t.Fatal(err)
	}
	if searchindex.MatchesV73(db) {
		t.Fatal("drifted index accepted")
	}
	for _, readOnly := range []bool{true, false} {
		s, err := NewSQLiteStore(Config{Enabled: true, Path: path, ReadOnly: readOnly})
		if err != nil {
			t.Fatal(err)
		}
		if s.hasSearchMessageIndex || s.searchMessageSource().Index != "" {
			t.Fatalf("read-only=%v: drifted index used as a hint", readOnly)
		}
		assertSearchFixtureVisibility(t, s)
		s.Close()
	}
	if err := migrateSearchIndexV73(db); err != nil || !searchindex.MatchesV73(db) {
		t.Fatalf("v73 did not restore the frozen index: %v", err)
	}
}

// The hinted search join must read only the index: SQLite omits WHERE terms
// that repeat the partial-index predicate, so no messages-table column is
// loaded and the deferred row seek never happens.
func TestSearchIndexV73Covering(t *testing.T) {
	s, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := s.searchMessageSource()
	if source.Index != searchindex.NameV73 {
		t.Fatalf("current schema did not select the index: %#v", source)
	}
	if op := searchJoinTableRead(t, s, "m.session_id, m.id, m.role = 'user'", source.Visible); op != "" {
		t.Fatalf("search join reads the messages table: %s", op)
	}
	// Control: a column outside the index does need the table row.
	if op := searchJoinTableRead(t, s, "m.session_id, m.created_at", source.Visible); op == "" {
		t.Fatal("table-read detection missed a non-covered column")
	}
}

// searchJoinTableRead returns the first opcode that reads the messages table
// in searchrank's hinted join shape, or "" when the index covers the query.
func searchJoinTableRead(t *testing.T, s *SQLiteStore, columns, visible string) string {
	t.Helper()
	var tableRoot int
	if err := s.db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'messages'`).Scan(&tableRoot); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`EXPLAIN WITH matched AS (SELECT rowid AS message_id FROM messages_fts WHERE messages_fts MATCH 'needle')
		SELECT ` + columns + ` FROM matched CROSS JOIN messages m INDEXED BY ` + searchindex.NameV73 +
		` ON m.id = matched.message_id WHERE ` + visible)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tableCursor, read := -1, ""
	for rows.Next() {
		var addr, p1, p2, p3, p5 int
		var opcode string
		var p4, comment sql.NullString
		if err := rows.Scan(&addr, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		switch {
		case opcode == "OpenRead" && p2 == tableRoot:
			tableCursor = p1
		case read == "" && tableCursor >= 0 && p1 == tableCursor &&
			(opcode == "Column" || opcode == "Rowid" || opcode == "NotExists" || strings.HasPrefix(opcode, "Seek")):
			read = opcode
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if tableCursor < 0 {
		t.Fatal("messages table cursor not found in the search plan")
	}
	return read
}
