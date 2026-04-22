package cmd

import (
	"testing"

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
