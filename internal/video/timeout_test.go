package video

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type videoDeadlineTransport func(*http.Request) (*http.Response, error)

func (f videoDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestVideoPaidDeadlineNoShortMethodCaps(t *testing.T) {
	for _, op := range []string{"quote", "queue", "retrieve", "complete"} {
		for _, duration := range []time.Duration{0, 2 * time.Hour, 10 * time.Second} {
			t.Run(fmt.Sprintf("%s/%s", op, duration), func(t *testing.T) {
				ctx := context.Background()
				var want time.Time
				if duration > 0 {
					var cancel context.CancelFunc
					want = time.Now().Add(duration)
					ctx, cancel = context.WithDeadline(ctx, want)
					defer cancel()
				}
				p := NewVeniceProvider("test-only")
				p.client = &http.Client{Transport: videoDeadlineTransport(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("no request deadline")
					} else if duration > 0 {
						if !deadline.Equal(want) {
							t.Error("nested method capped caller")
						}
					} else {
						left := time.Until(deadline)
						if left > time.Hour || left < time.Hour-10*time.Second {
							t.Error("short default", left)
						}
					}
					body := `{"quote":1}`
					switch op {
					case "queue":
						body = `{"model":"synthetic","queue_id":"job"}`
					case "retrieve":
						body = `{"status":"PROCESSING"}`
					case "complete":
						body = `{"success":true}`
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				req := Request{Model: "synthetic", Duration: "5s"}
				job := QueuedJob{"synthetic", "job"}
				var err error
				switch op {
				case "quote":
					_, err = p.Quote(ctx, req)
				case "queue":
					_, err = p.Queue(ctx, req)
				case "retrieve":
					_, err = p.RetrieveTo(ctx, job, io.Discard, false)
				case "complete":
					err = p.Complete(ctx, job)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
