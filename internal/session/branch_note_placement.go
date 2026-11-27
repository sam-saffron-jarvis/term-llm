package session

import "context"

type pathNotePrefixKey struct{}

// WithPathNotePrefix prevents an auto-sequenced note append if any rows beyond
// the copied prefix have appeared. SQLite checks this inside the append transaction.
func WithPathNotePrefix(ctx context.Context, forkAfterSequence int) context.Context {
	return context.WithValue(ctx, pathNotePrefixKey{}, forkAfterSequence)
}
