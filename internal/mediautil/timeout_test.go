package mediautil

import (
	"context"
	"testing"
	"time"
)

func TestMediaTimeoutDefaultAndCallerChoices(t *testing.T) {
	ctx, cancel := WithMediaTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Hour || time.Until(deadline) < time.Hour-time.Second {
		t.Fatal(deadline, ok)
	}
	for _, duration := range []time.Duration{10 * time.Second, 2 * time.Hour} {
		parent, stop := context.WithDeadline(context.Background(), time.Now().Add(duration))
		next, done := WithMediaTimeout(parent)
		want, _ := parent.Deadline()
		got, _ := next.Deadline()
		if !got.Equal(want) {
			t.Errorf("%s capped", duration)
		}
		stop()
		if next.Err() != context.Canceled {
			t.Error("lost parent cancellation")
		}
		done()
	}
}
