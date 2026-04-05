package llm

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestNewEventStreamReturnsRunErrorAfterBufferedEventsWhenBufferIsFull(t *testing.T) {
	filled := make(chan struct{})
	wantErr := errors.New("boom")
	bufSize := 0

	stream := newEventStream(context.Background(), func(ctx context.Context, ch chan<- Event) error {
		bufSize = cap(ch)
		for range bufSize {
			ch <- Event{Type: EventTextDelta, Text: "x"}
		}
		close(filled)
		return wantErr
	})

	<-filled

	for i := range bufSize {
		event, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv() %d error = %v", i, err)
		}
		if event.Type == EventError {
			t.Fatalf("Recv() %d unexpectedly returned error event: %v", i, event.Err)
		}
	}

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv() terminal error = %v, want nil", err)
	}
	if event.Type != EventError {
		t.Fatalf("Recv() terminal event type = %v, want %v", event.Type, EventError)
	}
	if !errors.Is(event.Err, wantErr) {
		t.Fatalf("Recv() terminal event error = %v, want %v", event.Err, wantErr)
	}

	event, err = stream.Recv()
	if err != io.EOF {
		t.Fatalf("final Recv() error = %v, want %v (event=%+v)", err, io.EOF, event)
	}
}
