package session

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestSearchSortBeforeLimit(t *testing.T) {
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insertion/relevance order deliberately differs from last-message activity,
	// creation and metadata update times. Pins must not override search ordering.
	for index, id := range []string{"middle", "new-b", "old", "new-a", "archived"} {
		sess := &Session{ID: id, Provider: "test", Model: "test", Mode: ModeChat, Origin: OriginWeb, CreatedAt: base.Add(time.Duration(10-index) * time.Hour), UpdatedAt: base, Pinned: id == "middle", Archived: id == "archived"}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatal(err)
		}
		if err := store.AddMessage(ctx, id, NewMessage(id, core.UserText("sortneedle match"), 0)); err != nil {
			t.Fatal(err)
		}
		activity := any(base.Add(5 * time.Hour))
		switch id {
		case "new-a", "new-b":
			activity = base.Add(9 * time.Hour)
		case "archived":
			activity = base.Add(20 * time.Hour)
		case "old":
			activity = nil // Falls back to creation; metadata update must not win.
		}
		created := base.Add(time.Hour)
		if id != "old" {
			created = base.Add(30 * time.Hour)
		}
		if _, err := store.db.ExecContext(ctx, "UPDATE sessions SET last_message_at = ?, created_at = ?, updated_at = ? WHERE id = ?", activity, created, base.Add(100*time.Hour), id); err != nil {
			t.Fatal(err)
		}
	}
	// A second matching message must not consume a result slot in date order.
	if err := store.AddMessage(ctx, "middle", NewMessage("middle", core.UserText("sortneedle match"), 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE sessions SET last_message_at = ? WHERE id = 'middle'", base.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		order    SearchSort
		limit    int
		archived bool
		want     []string
	}{
		{"default relevance", "", 1, false, []string{"middle"}},
		{"explicit relevance", SearchSortRelevance, 1, false, []string{"middle"}},
		{"newest before limit", SearchSortNewest, 1, false, []string{"new-a"}},
		{"oldest before limit", SearchSortOldest, 1, false, []string{"old"}},
		{"newest stable ties and distinct sessions", SearchSortNewest, 4, false, []string{"new-a", "new-b", "middle", "old"}},
		{"oldest stable ties", SearchSortOldest, 4, false, []string{"old", "middle", "new-a", "new-b"}},
		{"archived filter before limit", SearchSortNewest, 1, true, []string{"archived"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := store.Search(ctx, SearchOptions{Query: "sortneedle", Sort: tc.order, Limit: tc.limit, Archived: tc.archived, Categories: []string{"web"}})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, result := range results {
				ids = append(ids, result.SessionID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("IDs = %v, want %v", ids, tc.want)
			}
		})
	}
	if _, err := store.Search(ctx, SearchOptions{Query: "sortneedle", Sort: "invalid"}); err == nil {
		t.Fatal("expected invalid sort error")
	}
	// Older read-only schemas have no last_message_at; keep their creation fallback.
	store.hasLastMessageAt = false
	results, err := store.Search(ctx, SearchOptions{Query: "sortneedle", Sort: SearchSortOldest, Limit: 1})
	if err != nil || len(results) != 1 || results[0].SessionID != "old" {
		t.Fatalf("legacy search = %#v, %v", results, err)
	}
}

func TestSearchRanksVisibleConversationAndTitles(t *testing.T) {
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	add := func(id, name, longTitle string, texts ...core.Message) *Session {
		t.Helper()
		s := &Session{ID: id, Name: name, GeneratedLongTitle: longTitle, Provider: "test", Model: "test"}
		if err := store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			if err := store.AddMessage(ctx, id, NewMessage(id, text, i)); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	add("hidden", "Audiobook picks", "", core.Message{Role: core.RoleSystem, Parts: core.UserText("performance discourse specs github").Parts},
		core.Message{Role: core.RoleDeveloper, Parts: core.UserText("performance discourse specs github").Parts},
		core.Message{Role: core.RoleTool, Parts: core.UserText("performance discourse specs github").Parts},
		core.UserText("\n\t[Context Compaction]\nperformance discourse specs github"))
	add("weak", "", "", core.UserText("discourse discourse github github"))
	add("partial", "", "", core.UserText("discourse specs performance"))
	add("all-in-body", "", "", core.UserText("discourse github"), core.AssistantText("specs and performance"))
	add("title-and-body", "", "Discourse GitHub Actions CI Speed", core.UserText("specs"), core.AssistantText("performance"))
	custom := add("title-only", "Performance Discourse Specs GitHub", "Retired Title")

	for _, query := range []string{"performance discourse specs github", "performance discourse specs githu", "PERFORMANCE discourse specs github discourse github"} {
		results, err := store.Search(ctx, SearchOptions{Query: query, Limit: 10, Prefix: true})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, result := range results {
			ids = append(ids, result.SessionID)
		}
		want := []string{"title-only", "title-and-body", "all-in-body", "partial"}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("search %q = %v, want %v", query, ids, want)
		}
		if r := results[0]; r.MessageID != 0 || !r.CreatedAt.IsZero() || r.Snippet != custom.Name {
			t.Fatalf("unexpected title-only match: %#v", r)
		}
		if r := results[1]; r.MessageID == 0 || (r.Snippet != "**specs**" && r.Snippet != "**performance**") {
			t.Fatalf("expected highlighted visible excerpt: %#v", r)
		}
	}
	for _, q := range []string{"retired title", "!!!", "performance missingword", "githu "} {
		results, err := store.Search(ctx, SearchOptions{Query: q})
		if err != nil || len(results) != 0 {
			t.Fatalf("search %q = %#v, %v; want no matches", q, results, err)
		}
	}
	custom.Name = "Renamed conversation"
	if err := store.Update(ctx, custom); err != nil {
		t.Fatal(err)
	}
	results, err := store.Search(ctx, SearchOptions{Query: "renamed conv", Limit: 1, Prefix: true})
	if err != nil || len(results) != 1 || results[0].SessionID != custom.ID {
		t.Fatalf("renamed prefix search = %#v, %v", results, err)
	}
	results, err = store.Search(ctx, SearchOptions{Query: "renamed conv\t", Prefix: true})
	if err != nil || len(results) != 0 {
		t.Fatalf("completed query unexpectedly expanded a prefix: %#v, %v", results, err)
	}
}

func TestSearchTitleUsesDisplayedFallback(t *testing.T) {
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := &Session{ID: "title", Name: "\t\u00a0", GeneratedShortTitle: "Alpine", Provider: "test", Model: "test"}
	if err := store.Create(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	got, err := store.Search(t.Context(), SearchOptions{Query: "alpine"})
	if err != nil || len(got) != 1 || got[0].Snippet != s.PreferredShortTitle() {
		t.Fatalf("search = %#v, %v, want displayed title %q", got, err, s.PreferredShortTitle())
	}
}

func TestSearchRareTermScopeAllowsTitleAndSplitEvidence(t *testing.T) {
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<90)
		INSERT INTO sessions(id,name,provider,model) SELECT printf('s%d',i), 'Work log','test','test' FROM n;
		UPDATE sessions SET name='needle' WHERE id='s1';
		INSERT INTO messages(session_id,role,parts,sequence,text_content) SELECT id,'user','[]',0,'common' FROM sessions;
		INSERT INTO messages(session_id,role,parts,sequence,text_content) VALUES ('s2','assistant','[]',1,'needle'),('s3','system','[]',1,'needle');`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.Search(t.Context(), SearchOptions{Query: "common needle"})
	if err != nil || len(r) != 2 || r[0].SessionID != "s1" || r[1].SessionID != "s2" {
		t.Fatalf("scoped search = %#v, %v", r, err)
	}
}
