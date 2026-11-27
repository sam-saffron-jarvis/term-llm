package session

import (
	"context"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// Path notes are developer rows, but the web transcript must deliver them so
// the notes card can render; other developer rows stay provider-only.
func TestTranscriptReadsIncludePathNotesButNotOtherDeveloperRows(t *testing.T) {
	store, sess := newTranscriptTestStore(t)
	ctx := context.Background()
	user := NewMessage(sess.ID, core.UserText("question"), -1)
	answer := NewMessage(sess.ID, core.AssistantText("answer"), -1)
	developer := NewMessage(sess.ID, core.Message{Role: core.RoleDeveloper, Parts: []core.Part{{Type: core.PartText, Text: "internal context"}}}, -1)
	note := NewPathNoteMessage(sess.ID, "- carried finding", core.PathNoteProvenance{SourceSessionID: "source", AnchorMessageID: 7}, -1)
	for _, msg := range []*Message{user, answer, developer, note} {
		if err := store.AddMessage(ctx, sess.ID, msg); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}
	want := []int64{user.ID, answer.ID, note.ID}
	whole := []TranscriptRange{{StartSeq: user.Sequence, StartID: user.ID, EndSeq: note.Sequence, EndID: note.ID}}

	for name, indexer := range map[string]interface {
		GetTranscriptSnapshot(context.Context, string) (TranscriptSnapshot, error)
		GetMessagesByTranscriptRanges(context.Context, string, []TranscriptRange) (int64, []Message, error)
	}{"sqlite": store, "fallback": NewFallbackTranscriptIndexer(store)} {
		snapshot, err := indexer.GetTranscriptSnapshot(ctx, sess.ID)
		if err != nil {
			t.Fatalf("%s GetTranscriptSnapshot: %v", name, err)
		}
		var items []int64
		for _, item := range snapshot.Items {
			items = append(items, item.ID)
			if item.ID == note.ID && item.Flags&TranscriptFlagEmptyBody != 0 {
				t.Fatalf("%s path note flagged as empty body: %#v", name, item)
			}
		}
		if !equalIDs(items, want) {
			t.Fatalf("%s index IDs = %v, want %v", name, items, want)
		}
		_, bodies, err := indexer.GetMessagesByTranscriptRanges(ctx, sess.ID, whole)
		if err != nil {
			t.Fatalf("%s GetMessagesByTranscriptRanges: %v", name, err)
		}
		var bodyIDs []int64
		for _, body := range bodies {
			bodyIDs = append(bodyIDs, body.ID)
		}
		if !equalIDs(bodyIDs, want) {
			t.Fatalf("%s body IDs = %v, want %v", name, bodyIDs, want)
		}
	}
}

func equalIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
