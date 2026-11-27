package branchnotes

import (
	"context"
	"slices"

	"github.com/term-llm/term-llm/internal/session"
)

// AwaitsFirstTurn reports whether a session holds path notes but no user turn
// yet: a /thread opened with notes. Its first request has no
// previous_response_id to continue from, yet treating that input as the whole
// conversation would replace the transcript and discard the notes.
func AwaitsFirstTurn(ctx context.Context, store session.Store, sessionID string) bool {
	if store == nil || sessionID == "" {
		return false
	}
	if sess, err := store.Get(ctx, sessionID); err != nil || sess == nil || sess.UserTurns != 0 {
		return false
	}
	messages, err := store.GetMessages(ctx, sessionID, 0, 0)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(messages, func(message session.Message) bool {
		_, ok := message.PathNoteProvenance()
		return ok
	})
}
