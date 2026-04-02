package llm

import (
	"context"
	"io"
)

type channelStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	events <-chan Event
}

func newEventStream(ctx context.Context, run func(context.Context, chan<- Event) error) Stream {
	streamCtx, cancel := context.WithCancel(ctx)
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		if err := run(streamCtx, ch); err != nil {
			// If the consumer has stopped draining and the buffer is full, drop the
			// terminal error rather than block forever and leak the producer goroutine.
			select {
			case ch <- Event{Type: EventError, Err: err}:
			case <-streamCtx.Done():
			default:
			}
		}
	}()
	return &channelStream{ctx: streamCtx, cancel: cancel, events: ch}
}

func (s *channelStream) Recv() (Event, error) {
	// Non-blocking drain: consume any buffered event before checking ctx.Done().
	// This prevents dropping EventUsage/EventDone when ctx and events are both ready.
	select {
	case event, ok := <-s.events:
		if !ok {
			return Event{}, io.EOF
		}
		return event, nil
	default:
	}

	select {
	case <-s.ctx.Done():
		return Event{}, s.ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return Event{}, io.EOF
		}
		return event, nil
	}
}

func (s *channelStream) Close() error {
	s.cancel()
	return nil
}
