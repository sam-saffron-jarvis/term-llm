package filetrack

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/samsaffron/term-llm/internal/llm"
)

// Recorder adapts a Store to the tools-facing FileChangeRecorder interface.
// Recording is best-effort: failures are reported once on stderr and never
// surface to the calling tool — tracking must not break editing.
type Recorder struct {
	store    *Store
	warnOnce sync.Once
}

// NewRecorder wraps a store in a Recorder.
func NewRecorder(store *Store) *Recorder {
	return &Recorder{store: store}
}

// RecordChange persists one before→after transition. Returns nil when the
// change is a no-op or recording fails.
func (r *Recorder) RecordChange(ctx context.Context, rec ChangeRecord) *llm.FileChange {
	change, err := r.store.RecordChange(ctx, rec)
	if err != nil {
		r.warnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "warning: file change tracking failed: %v\n", err)
		})
		return nil
	}
	if change == nil {
		return nil
	}
	return &llm.FileChange{
		Path:      change.Path,
		Kind:      change.Kind,
		Adds:      change.Adds,
		Dels:      change.Dels,
		Seq:       change.Seq,
		Truncated: change.Truncated,
	}
}

// SessionPaths returns paths already recorded for the session (best-effort).
func (r *Recorder) SessionPaths(ctx context.Context, sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	paths, err := r.store.SessionPaths(ctx, sessionID)
	if err != nil {
		return nil
	}
	return paths
}

// MaxFileBytes returns the per-file content cap.
func (r *Recorder) MaxFileBytes() int {
	return r.store.MaxFileBytes()
}
