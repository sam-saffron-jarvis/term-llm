package signal

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// NotifyContext returns a context that is cancelled when SIGINT or SIGTERM is received.
// The returned stop function should be called to release resources.
func NotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
