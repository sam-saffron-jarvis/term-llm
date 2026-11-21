package usagetap

import "time"

// MaxWebSocketTurns bounds the total callbacks from one WebSocket, including
// turns emitted by Finish. Once full, the observer ignores new response IDs
// without evicting previously seen IDs or abandoning already observed starts.
const MaxWebSocketTurns = maxResponseIDs

// WebSocketTurn is one immutable, server-observed Responses turn. It never
// exposes the response ID. Outcome is "complete", "incomplete", or "unknown".
// Started and Ended are local observation times. Started is zero if no start
// was observed. Usage contains only the first terminal snapshot's
// evidence (or unknown counts if no terminal was observed), with Turns set to 1.
type WebSocketTurn struct {
	Started, Ended time.Time
	Usage          Usage
	Outcome        string
}

// SetTurnObserver opts into per-turn callbacks for a Responses WebSocket.
// Set it before the first Feed (nil disables it); later calls have no effect.
// The observer runs synchronously in Feed or Finish and must not call back into
// the tap. The first terminal event for a nonempty response ID of at most 256
// bytes emits once; subsequent snapshots cannot change the emitted turn. This
// does not affect the aggregate, which can still accept later snapshots.
func (w *WebSocket) SetTurnObserver(observe func(WebSocketTurn)) {
	if w.started || w.parser.finished || w.parser.protocol != Responses {
		return
	}
	w.turns = nil
	if observe != nil {
		w.turns = &webSocketTurns{observe: observe, starts: make(map[string]time.Time)}
	}
}

type webSocketTurns struct {
	observe  func(WebSocketTurn)
	starts   map[string]time.Time // A zero time is a tombstone, not an open turn.
	afterGap bool
}

func (t *webSocketTurns) lookup(id string) (start time.Time, eligible bool) {
	if id == "" || len(id) > maxResponseIDBytes {
		return time.Time{}, false
	}
	start, seen := t.starts[id]
	if seen {
		return start, !start.IsZero()
	}
	return time.Time{}, len(t.starts) < MaxWebSocketTurns
}

func (t *webSocketTurns) start(id string, created bool) {
	if started, eligible := t.lookup(id); eligible && started.IsZero() {
		if t.afterGap && !created {
			// This ID may have started or even finished in the skipped message.
			t.starts[id] = time.Time{}
			return
		}
		t.starts[id] = time.Now()
	}
}

func (t *webSocketTurns) end(id, outcome string, u Usage, bad bool) {
	started, eligible := t.lookup(id)
	if !eligible {
		return
	}
	t.starts[id] = time.Time{}
	if t.afterGap && started.IsZero() {
		return // A terminal alone is no longer reliable first-terminal evidence.
	}
	ended := time.Now()
	if u.Ambiguous {
		u.Model, u.Tier = "", ""
	}
	if !bad && hasCounts(u) {
		u.Source = Partial
		if outcome == "complete" {
			u.Source = Reported
		}
	}
	u.Turns = 1
	t.observe(WebSocketTurn{Started: started, Ended: ended, Usage: u, Outcome: outcome})
}

// A skipped message could have ended any pending turn. Finalize each as
// incomplete once; only a fresh, explicit creation can establish a new turn.
func (t *webSocketTurns) gap() bool {
	t.afterGap = true
	return t.finish()
}

func (t *webSocketTurns) finish() bool {
	emitted := false
	ended := time.Now()
	u := emptyUsage()
	u.Turns = 1
	for id, started := range t.starts {
		if !started.IsZero() {
			emitted = true
			t.starts[id] = time.Time{}
			t.observe(WebSocketTurn{Started: started, Ended: ended, Usage: u, Outcome: "incomplete"})
		}
	}
	return emitted
}

func webSocketTurnOutcome(typ, status string) string {
	if typ == "response.failed" || typ == "response.incomplete" {
		return "incomplete"
	}
	switch status {
	case "failed", "incomplete", "cancelled", "canceled":
		return "incomplete"
	case "completed":
		return "complete"
	case "":
		if typ == "response.completed" {
			return "complete"
		}
	}
	return "unknown"
}
