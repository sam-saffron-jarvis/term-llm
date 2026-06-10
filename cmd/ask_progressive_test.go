package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/ui"
)

func TestAskProgressiveBridge_PropagatesInterjectionID(t *testing.T) {
	bridge := newAskProgressiveBridge(ui.DefaultStreamBufferSize)

	if err := bridge.HandleEvent(llm.Event{
		Type:           llm.EventInterjection,
		Text:           "keep sleeping",
		InterjectionID: "bridge-interject-1",
	}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	ev := <-bridge.Events()
	if ev.Type != ui.StreamEventInterjection {
		t.Fatalf("event type = %v, want %v", ev.Type, ui.StreamEventInterjection)
	}
	if ev.Text != "keep sleeping" {
		t.Fatalf("event text = %q, want %q", ev.Text, "keep sleeping")
	}
	if ev.InterjectionID != "bridge-interject-1" {
		t.Fatalf("event interjection ID = %q, want %q", ev.InterjectionID, "bridge-interject-1")
	}
}

func TestAskProgressiveBridge_ShutdownPreventsBlockedSends(t *testing.T) {
	bridge := newAskProgressiveBridge(1)
	if err := bridge.HandleEvent(llm.Event{Type: llm.EventTextDelta, Text: "buffered"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	bridge.Shutdown()

	handleDone := make(chan error, 1)
	go func() {
		handleDone <- bridge.HandleEvent(llm.Event{Type: llm.EventTextDelta, Text: "blocked"})
	}()

	select {
	case err := <-handleDone:
		if !errors.Is(err, errAskProgressiveBridgeClosed) {
			t.Fatalf("HandleEvent() error = %v, want %v", err, errAskProgressiveBridgeClosed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("HandleEvent() blocked after Shutdown")
	}

	closeDone := make(chan struct{})
	go func() {
		bridge.CloseSuccess()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("CloseSuccess() blocked after Shutdown")
	}
}
