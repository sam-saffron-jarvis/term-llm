package branchnotes

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/session"
)

func IdleStatusCode(ctx context.Context, store session.Store, childID string) int {
	if store == nil {
		return http.StatusNotFound
	}
	sess, err := store.Get(ctx, childID)
	if errors.Is(err, session.ErrNotFound) {
		return http.StatusNotFound
	}
	if err != nil {
		return http.StatusInternalServerError
	}
	if sess == nil {
		return http.StatusNotFound
	}
	return http.StatusOK
}

func (r *Registry) Cancel(ctx context.Context, store session.Store, edge session.BranchTreeNode) (wire.PathNotesCancellation, error) {
	saving := r.skip(edge.SessionID)
	if saving != nil {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-saving.Done():
		case <-timer.C:
			return wire.PathNotesCancellation{Status: saving.Snapshot()}, nil
		case <-ctx.Done():
			return wire.PathNotesCancellation{}, ctx.Err()
		}
	}
	messages, err := store.GetMessages(ctx, edge.SessionID, 0, 0)
	if err != nil {
		return wire.PathNotesCancellation{}, err
	}
	saved := ExistingPathNote(messages, edge.ParentSessionID, edge.ForkAfterMessageID)
	return wire.PathNotesCancellation{NoteSaved: saved, Cancelled: saving == nil && !saved}, nil
}

func WorkError(ctx context.Context) (int, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "branch context generation timed out"
	}
	return http.StatusConflict, "branch context generation was cancelled"
}
