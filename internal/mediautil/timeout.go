package mediautil

import (
	"context"
	"time"
)

// DefaultMediaTimeout allows slow paid generation to finish. A local deadline
// does not cancel upstream work or undo its charge.
const DefaultMediaTimeout = time.Hour

// WithMediaTimeout supplies a fallback only when the caller has no deadline.
// Explicit longer and shorter deadlines remain the caller's choice; transports
// must not impose a hidden, shorter response/body timeout of their own.
func WithMediaTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, DefaultMediaTimeout)
}
