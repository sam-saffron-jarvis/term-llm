package wire

import (
	"github.com/term-llm/term-llm/internal/llm/core"
)

// PendingClientCallTracker records which client (passthrough) tool calls a run
// hands back to its caller unanswered.
//
// The engine only stops on passthrough calls made by the final provider turn,
// and only when that turn deferred no server tool to its end-of-turn split;
// client calls that share a turn with such a server tool are dropped. Callers cannot reliably reconstruct
// this from the event stream, because tool-only turns emit no text boundary,
// so the server reports the list explicitly on completion.
type PendingClientCallTracker struct {
	turn       int
	turnSet    bool
	serverTool bool
	calls      []core.ToolCall
}

func (t *PendingClientCallTracker) resetTurn() {
	t.serverTool = false
	t.calls = nil
}

// Observe records the provider turn that offered each passthrough call.
func (t *PendingClientCallTracker) Observe(event core.Event, isServerTool func(string) bool) {
	if event.ProviderTurnIndexSet && (!t.turnSet || event.ProviderTurnIndex > t.turn) {
		t.turn, t.turnSet = event.ProviderTurnIndex, true
		t.resetTurn()
	}
	switch event.Type {
	case core.EventAttemptDiscard:
		// A discarded attempt is retried as the same provider turn.
		if !event.ProviderTurnIndexSet || event.ProviderTurnIndex == t.turn {
			t.resetTurn()
		}
	case core.EventToolCall:
		// Inline calls ran during the provider stream and never enter the
		// engine's end-of-turn split, so they neither stop the run nor cause
		// passthrough calls in their turn to be dropped.
		if event.Tool == nil || event.ToolInline {
			return
		}
		if isServerTool != nil && isServerTool(event.Tool.Name) {
			t.serverTool = true
			return
		}
		for _, existing := range t.calls {
			if existing.ID == event.Tool.ID {
				return
			}
		}
		t.calls = append(t.calls, *event.Tool)
	}
}

// Pending returns the client calls awaiting results, in call order. It is
// never nil so that callers can publish an authoritative, possibly empty, list.
func (t *PendingClientCallTracker) Pending() []core.ToolCall {
	if t.serverTool || len(t.calls) == 0 {
		return []core.ToolCall{}
	}
	return append([]core.ToolCall(nil), t.calls...)
}

// PendingClientCallsPayload is the wire form published on response.completed.
func PendingClientCallsPayload(calls []core.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		arguments := string(call.Arguments)
		if arguments == "" {
			arguments = "{}"
		}
		out = append(out, map[string]any{
			"call_id":   call.ID,
			"name":      call.Name,
			"arguments": arguments,
		})
	}
	return out
}

// PendingClientCallsValue reads a published pending list, native or JSON
// decoded. It returns nil when the value is not a list.
func PendingClientCallsValue(value any) []map[string]any {
	switch calls := value.(type) {
	case []map[string]any:
		return calls
	case []any:
		out := make([]map[string]any, 0, len(calls))
		for _, entry := range calls {
			if call, ok := entry.(map[string]any); ok {
				out = append(out, call)
			}
		}
		return out
	}
	return nil
}

// ToolResultIDs reports every supplied tool result, including duplicates.
func ToolResultIDs(input []core.Message) []string {
	var calls []string
	for _, msg := range input {
		for _, part := range msg.Parts {
			if part.Type == core.PartToolResult && part.ToolResult != nil {
				calls = append(calls, part.ToolResult.ID)
			}
		}
	}
	return calls
}

// ExactToolCallIDs compares non-empty sets of unique call IDs without relying on order.
func ExactToolCallIDs(pending, calls []string) bool {
	if len(pending) == 0 || len(pending) != len(calls) {
		return false
	}
	ids := make(map[string]bool, len(pending))
	for _, id := range pending {
		if id == "" || ids[id] {
			return false
		}
		ids[id] = true
	}
	for _, id := range calls {
		if !ids[id] {
			return false
		}
		delete(ids, id)
	}
	return true
}

// PendingClientCallIDs extracts the IDs in the published pending list.
func PendingClientCallIDs(pending []map[string]any) []string {
	var ids []string
	for _, call := range pending {
		if id, _ := call["call_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
