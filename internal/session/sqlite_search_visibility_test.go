package session

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session/searchindex"
)

// The frozen SQL must classify legacy goal prompts exactly as
// Message.IsGoalSteering does, with and without the client_message_id column.
func TestSearchVisibleMessageSQLMatchesGoalSteering(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE messages(id INTEGER PRIMARY KEY, role TEXT, text_content TEXT, compaction_tail BOOLEAN, client_message_id TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, prefix := range core.LegacyGoalSteeringPrefixes() {
		texts = append(texts, prefix, " \n\t"+prefix+"\n\nobjective", prefix[:len(prefix)-1],
			"> "+prefix, "Quoting: "+prefix, strings.ToUpper(prefix), strings.Replace(prefix, "\n\n", "\n", 1))
	}
	texts = append(texts, "", "ordinary prompt")
	type row struct {
		id       int64
		role     core.Role
		clientID string
		text     string
	}
	var rows []row
	for _, text := range texts {
		for _, role := range []core.Role{core.RoleUser, core.RoleAssistant} {
			for _, clientID := range []string{"", " ", "client-1"} {
				res, err := db.Exec(`INSERT INTO messages(role, text_content, client_message_id) VALUES (?, ?, ?)`, role, text, clientID)
				if err != nil {
					t.Fatal(err)
				}
				id, _ := res.LastInsertId()
				rows = append(rows, row{id, role, clientID, text})
			}
		}
	}
	visible := func(predicate string) map[int64]bool {
		t.Helper()
		result, err := db.Query(`SELECT id FROM messages m WHERE ` + predicate)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		ids := map[int64]bool{}
		for result.Next() {
			var id int64
			if err := result.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids[id] = true
		}
		return ids
	}
	withClientID := visible(searchindex.VisibleSQL("m", true, true))
	withoutClientID := visible(searchindex.VisibleSQL("m", true, false))
	for _, r := range rows {
		m := &Message{Role: r.role, ClientMessageID: r.clientID, TextContent: r.text, Parts: core.UserText(r.text).Parts}
		want := !m.IsGoalSteering()
		if got := withClientID[r.id]; got != want {
			t.Errorf("role=%s client=%q text=%q: visible=%v, want %v", r.role, r.clientID, r.text, got, want)
		}
		// Old databases have no client IDs, so every legacy prompt is internal.
		m.ClientMessageID = ""
		if got, want := withoutClientID[r.id], !m.IsGoalSteering(); got != want {
			t.Errorf("no client column: role=%s text=%q: visible=%v, want %v", r.role, r.text, got, want)
		}
	}
}

// Read-only search of a database that predates client message IDs still
// hides legacy goal prompts, without naming the search index.
func TestSearchLegacyGoalPromptWithoutClientIDColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	fixture := strings.ReplaceAll(searchFixtureSQL, ",client_message_id) VALUES", ") VALUES")
	fixture = strings.NewReplacer(",'')", ")", ",'client-1')", ")").Replace(fixture)
	if _, err := db.Exec(`DROP INDEX idx_messages_search_visible; DROP INDEX idx_messages_client_message_id;
		ALTER TABLE messages DROP COLUMN client_message_id; UPDATE schema_version SET version=72;` + fixture); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(Config{Enabled: true, Path: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.hasMessageClientID || s.searchMessageSource().Index != "" {
		t.Fatalf("legacy columns misdetected: client=%v source=%#v", s.hasMessageClientID, s.searchMessageSource())
	}
	for query, want := range map[string][]string{
		"sentinel": {}, "kiwi": {}, "mango": {}, "papaya": {"inline"}, "alpine glacier": {"fixture"},
	} {
		if got := searchSessionIDs(t, s, query); !reflect.DeepEqual(got, want) {
			t.Fatalf("search %q = %v, want %v", query, got, want)
		}
	}
}
