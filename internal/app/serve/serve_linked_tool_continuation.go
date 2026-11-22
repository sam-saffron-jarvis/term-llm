package serve

import (
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
)

// Non-inline providers return tool calls at the end of a response. The page
// posts their output as a new response rather than to the inline mailbox.
// Apply the same ownership boundary to that handoff. Its cursor may be the
// durable transcript ID published on completion, not the streaming run ID.
func (s *serveServer) allowLinkedToolContinuation(w http.ResponseWriter, r *http.Request, previous string, input []core.Message) bool {
	peer, linked := linkeddevices.DeviceFromRequest(r)
	if !linked {
		return true
	}
	var calls []string
	for _, msg := range input {
		for _, part := range msg.Parts {
			if part.Type == core.PartToolResult && part.ToolResult != nil {
				calls = append(calls, part.ToolResult.ID)
			}
		}
	}
	if len(calls) == 0 {
		return true
	}
	previous = strings.TrimSpace(previous)
	if previous != "" && peer.Lifetime != nil && peer.Lifetime.Err() == nil &&
		s.ensureResponseRuns().ownsLinkedToolHandoff(peer.DeviceID, previous, calls) {
		return true
	}
	writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "client tool handoff not found")
	return false
}

func (m *responseRunManager) ownsLinkedToolHandoff(deviceID, previous string, calls []string) bool {
	m.mu.Lock()
	var candidates []*responseRun
	if run := m.runs[previous]; run != nil {
		candidates = append(candidates, run)
	} else if _, durable := parseDurableResponseMessageID(previous); durable {
		for _, run := range m.runs {
			if run.linkedDeviceID == deviceID {
				candidates = append(candidates, run)
			}
		}
	}
	m.mu.Unlock()
	for _, run := range candidates {
		if run.linkedDeviceID == deviceID && run.matchesToolHandoff(previous, calls) {
			return true
		}
	}
	return false
}

func (r *responseRun) matchesToolHandoff(previous string, calls []string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != "completed" || (previous != r.id && previous != r.continuationResponseID) {
		return false
	}
	pending := make(map[string]bool, len(r.pendingClientCalls))
	for _, call := range r.pendingClientCalls {
		if id := stringValue(call["call_id"]); id != "" {
			pending[id] = true
		}
	}
	for _, id := range calls {
		if !pending[id] {
			return false
		}
		delete(pending, id)
	}
	return true
}
