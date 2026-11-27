package server

import (
	"context"
	"sync"
)

// AdmissionGate serializes preparation per key without retaining idle keys.
// Its zero value is ready to use.
type AdmissionGate struct {
	mu      sync.Mutex
	pending map[string]chan struct{}
}

// Acquire waits for the key or context cancellation. Its release is idempotent.
func (g *AdmissionGate) Acquire(ctx context.Context, key string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		if pending, ok := g.pending[key]; ok {
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		if g.pending == nil {
			g.pending = make(map[string]chan struct{})
		}
		pending := make(chan struct{})
		g.pending[key] = pending
		g.mu.Unlock()
		return sync.OnceFunc(func() {
			g.mu.Lock()
			delete(g.pending, key)
			close(pending)
			g.mu.Unlock()
		}), nil
	}
}
