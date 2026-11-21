package usagetap

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"
)

func feedTurnEvent(w *WebSocket, event string) {
	w.Feed(frame(1, true, []byte(event)))
}

func TestWebSocketTurnsFirstTerminalIsImmutable(t *testing.T) {
	p := New(Responses, JSON)
	w := NewWebSocket(p)
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })

	beforeStart := time.Now()
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"r1","usage":{"input_tokens":999}}}`)
	afterStart := time.Now()
	feedTurnEvent(w, `{"type":"response.in_progress","response":{"id":"r2"}}`)
	feedTurnEvent(w, `{"type":"response.in_progress","response":{"id":"r1"}}`)
	feedTurnEvent(w, `{"type":"response.output_text.done","response":{"id":"r1"}}`)
	if len(turns) != 0 {
		t.Fatalf("nonterminal event emitted: %+v", turns)
	}

	first := []byte(strings.Replace(responseJSON, `"input_tokens":12`, `"input_tokens":10`, 1))
	mid := len(first) / 2
	w.Feed(frame(1, false, first[:mid]))
	w.Feed(frame(9, true, []byte("ping")))
	beforeEnd := time.Now()
	for _, b := range frame(0, true, first[mid:]) {
		w.Feed([]byte{b})
	}
	afterEnd := time.Now()
	if len(turns) != 1 {
		t.Fatalf("first terminal did not synchronously emit: %+v", turns)
	}
	firstTurn := turns[0]
	if firstTurn.Started.Before(beforeStart) || firstTurn.Started.After(afterStart) ||
		firstTurn.Ended.Before(beforeEnd) || firstTurn.Ended.After(afterEnd) {
		t.Fatalf("expected local first-start and terminal times, got %+v", firstTurn)
	}
	if u := firstTurn.Usage; firstTurn.Outcome != "complete" || u.Input != 10 || u.Output != 7 || u.Total != 19 || u.CachedInput != 4 || u.Reasoning != 2 || u.CacheWrite != UnknownTokens || u.Source != Reported || u.Turns != 1 {
		t.Fatalf("first turn did not preserve exact counters: %+v", firstTurn)
	}

	feedTurnEvent(w, responseJSON) // Revised aggregate counts supersede the first snapshot.
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"r1"}}`)
	feedTurnEvent(w, `{"type":"response.completed","response":{"id":"r2","model":"other-model","service_tier":"priority","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`)
	if len(turns) != 2 || turns[0] != firstTurn || turns[1].Started.IsZero() {
		t.Fatalf("replay changed callbacks: %+v", turns)
	}
	if u := turns[1].Usage; u.Input != 3 || u.Output != 2 || u.Total != 5 || u.CachedInput != UnknownTokens || u.Model != "other-model" || u.Tier != "priority" || u.Ambiguous {
		t.Fatalf("second turn inherited other usage: %+v", turns[1])
	}
	if u := w.Finish(false); u.Source != Reported || u.Input != 15 || u.Output != 9 || u.Total != 24 || u.Turns != 2 || !u.Ambiguous || len(turns) != 2 {
		t.Fatalf("legacy aggregate or replay behavior changed: %+v, turns %+v", u, turns)
	}
}

func TestWebSocketTurnTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, typ, response, outcome string
		source                       Source
		input                        int64
	}{
		{"no usage", "completed", ``, "complete", Unknown, UnknownTokens},
		{"incomplete", "incomplete", `,"usage":{"input_tokens":3}`, "incomplete", Partial, 3},
		{"failed", "failed", `,"usage":{"input_tokens":3}`, "incomplete", Partial, 3},
		{"cancelled", "cancelled", `,"usage":{"input_tokens":3}`, "incomplete", Partial, 3},
		{"canceled", "canceled", ``, "incomplete", Unknown, UnknownTokens},
		{"done complete", "done", `,"status":"completed","usage":{"input_tokens":0}`, "complete", Reported, 0},
		{"done incomplete", "done", `,"status":"incomplete","usage":{"input_tokens":3}`, "incomplete", Partial, 3},
		{"done unknown", "done", `,"usage":{"input_tokens":3}`, "unknown", Partial, 3},
		{"invalid counter", "completed", `,"usage":{"input_tokens":3,"output_tokens":-1}`, "complete", Unknown, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWebSocket(New(Responses, JSON))
			var turns []WebSocketTurn
			w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
			event := fmt.Sprintf(`{"type":"response.%s","response":{"id":"r1"%s}}`, tc.typ, tc.response)
			feedTurnEvent(w, event)
			feedTurnEvent(w, responseJSON) // No amendment, even if the first terminal lacked counters.
			w.Finish(true)
			if len(turns) != 1 {
				t.Fatalf("got %d turns: %+v", len(turns), turns)
			}
			turn := turns[0]
			if turn.Outcome != tc.outcome || turn.Usage.Source != tc.source || turn.Usage.Input != tc.input || turn.Usage.Turns != 1 || !turn.Started.IsZero() || turn.Ended.IsZero() {
				t.Fatalf("unexpected terminal-only turn: %+v", turn)
			}
		})
	}
}

func TestWebSocketTurnsFinishObservedStartsOnly(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"canceled"}}`)
	feedTurnEvent(w, `{"type":"response.canceled","response":{"id":"canceled"}}`)
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"pending","usage":{"input_tokens":999}}}`)
	feedTurnEvent(w, `{"type":"response.cancelled","response":{}}`)
	feedTurnEvent(w, `{"type":"error","response":{"id":"pending"}}`)
	// A terminal frame in the client direction must not close the pending turn.
	payload := []byte(`{"type":"response.completed","response":{"id":"pending"}}`)
	const key = 0x01020304
	masked := binary.BigEndian.AppendUint32([]byte{129, 128 | byte(len(payload))}, key)
	for i, b := range payload {
		masked = append(masked, b^byte(i%4+1))
	}
	w.Feed(masked)
	// Nor does a partially received server message count as a terminal.
	w.Feed(frame(1, false, payload))
	if len(turns) != 1 || turns[0].Outcome != "incomplete" || turns[0].Started.IsZero() {
		t.Fatalf("unexpected immediate callbacks: %+v", turns)
	}
	beforeFinish := time.Now()
	first := w.Finish(true)
	if got := w.Finish(false); got != first {
		t.Fatalf("Finish changed aggregate: %+v vs %+v", first, got)
	}
	feedTurnEvent(w, responseJSON)
	if len(turns) != 2 {
		t.Fatalf("unexpected Finish callbacks: %+v", turns)
	}
	pending := turns[1]
	if pending.Outcome != "incomplete" || pending.Started.IsZero() || pending.Started.After(beforeFinish) || pending.Ended.Before(beforeFinish) || pending.Usage.Source != Unknown || hasCounts(pending.Usage) || pending.Usage.Turns != 1 {
		t.Fatalf("unexpected unfinished turn: %+v", pending)
	}
}

func TestWebSocketTurnsBoundedAcrossFeedAndFinish(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
	feedTurnEvent(w, `{"type":"response.completed","response":{"usage":{"input_tokens":1}}}`)
	feedTurnEvent(w, fmt.Sprintf(`{"type":"response.completed","response":{"id":%q}}`, strings.Repeat("x", 257)))
	if len(turns) != 0 || w.parser.Snapshot().Input != 1 {
		t.Fatalf("invalid ID emitted a turn or missing ID didn't reach aggregate: %+v", turns)
	}
	w.SetTurnObserver(func(WebSocketTurn) { t.Fatal("replaced observer after Feed") })
	for i := 0; i < MaxWebSocketTurns-1; i++ {
		feedTurnEvent(w, fmt.Sprintf(`{"type":"response.completed","response":{"id":"r%d"}}`, i))
	}
	feedTurnEvent(w, fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, strings.Repeat("x", 256)))
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"overflow-start"}}`)
	feedTurnEvent(w, `{"type":"response.completed","response":{"id":"overflow-terminal"}}`)
	feedTurnEvent(w, `{"type":"response.completed","response":{"id":"r0"}}`)
	if len(turns) != MaxWebSocketTurns-1 {
		t.Fatalf("overflow or replay emitted extra turns: %d", len(turns))
	}
	w.Finish(false)
	if len(turns) != MaxWebSocketTurns || turns[len(turns)-1].Outcome != "incomplete" {
		t.Fatalf("capacity did not preserve the tracked pending turn: %+v", turns)
	}
}

func TestWebSocketTurnsResponsesOnly(t *testing.T) {
	w := NewWebSocket(New(ChatCompletions, JSON))
	w.SetTurnObserver(func(WebSocketTurn) { t.Fatal("non-Responses WebSocket emitted a turn") })
	feedTurnEvent(w, responseJSON)
	w.Finish(true)
}

func TestWebSocketTurnRecoveryAfterOversizedMessage(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
	event := func(kind, id string) {
		feedTurnEvent(w, fmt.Sprintf(`{"type":"response.%s","response":{"id":%q,"usage":{"input_tokens":12}}}`, kind, id))
	}
	event("created", "open")
	w.Feed(frame(1, false, make([]byte, MaxEventBytes)))
	w.Feed(frame(0, false, []byte{'x'})) // Cumulative size crosses the limit.
	w.Feed(frame(9, true, []byte("ping")))
	w.Feed(frame(0, true, []byte(responseJSON))) // Still part of the skipped message.
	if w.message != nil || len(turns) != 1 || turns[0].Outcome != "incomplete" || turns[0].Usage.Source != Unknown || turns[0].Usage.Input != UnknownTokens {
		t.Fatalf("gap did not close the pending turn conservatively: %+v", turns)
	}
	for _, tc := range []struct{ kind, id string }{{"completed", "open"}, {"completed", "terminal-only"}, {"in_progress", "already-running"}} {
		event(tc.kind, tc.id)
		event("created", tc.id) // Replays can't turn ambiguous evidence into charges.
		event("completed", tc.id)
	}
	event("created", "new")
	event("completed", "new")
	event("completed", "new")
	if u := w.Finish(true); u.Source != Unknown || len(turns) != 2 || turns[1].Outcome != "complete" || turns[1].Usage.Source != Reported || turns[1].Usage.Input != 12 {
		t.Fatalf("aggregate=%+v turns=%+v", u, turns)
	}
}

func TestWebSocketSkipsOnlyWellFramedOversizedMessages(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"pending"}}`)
	w.Feed(frame(1, false, make([]byte, MaxEventBytes+1)))
	if w.disabled || w.message != nil || !w.fragmented {
		t.Fatal("did not skip the oversized fragment at bounded memory")
	}
	// A text start instead of a continuation is still a protocol error.
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"r1"}}`)
	feedTurnEvent(w, responseJSON)
	if u := w.Finish(true); !w.disabled || u.Source != Unknown || len(turns) != 1 || turns[0].Outcome != "incomplete" {
		t.Fatalf("invalid frame sequence did not disable observation: %+v", u)
	}
}

func TestWebSocketUnattributableGapStaysUnknown(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	w.SetTurnObserver(func(turn WebSocketTurn) { t.Fatalf("unattributed gap hidden by %+v", turn) })
	w.Feed(frame(1, true, make([]byte, MaxEventBytes+1)))
	feedTurnEvent(w, `{"type":"response.created","response":{"id":"r1"}}`)
	feedTurnEvent(w, responseJSON)
	if u := w.Finish(true); u.Source != Unknown || u.Turns != 0 {
		t.Fatalf("gap reported usage: %+v", u)
	}
}
