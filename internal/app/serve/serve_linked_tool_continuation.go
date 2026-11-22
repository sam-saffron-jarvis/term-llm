package serve

import (
	"context"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session/transcript"
)

// Non-inline providers return tool calls at the end of a response. The page
// posts their output as a new response rather than to the inline mailbox.
// Apply the same ownership boundary to that handoff. Its cursor may be the
// durable transcript ID published on completion, not the streaming run ID.
func (s *serveServer) allowLinkedToolContinuation(w http.ResponseWriter, r *http.Request, previous string, branch bool, input []core.Message) bool {
	peer, linked := linkeddevices.DeviceFromRequest(r)
	if !linked {
		return true
	}
	calls := wire.ToolResultIDs(input)
	if len(calls) == 0 {
		return true
	}
	previous = strings.TrimSpace(previous)
	if !branch && previous != "" && peer.Lifetime != nil && peer.Lifetime.Err() == nil {
		owned := s.ensureResponseRuns().ownsLinkedToolHandoff(peer.DeviceID, previous, calls) ||
			s.ownsDurableLinkedToolHandoff(r.Context(), peer.DeviceID, previous, calls)
		if owned && peer.Lifetime.Err() == nil {
			return true
		}
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
	return wire.ExactToolCallIDs(wire.PendingClientCallIDs(r.pendingClientCalls), calls)
}

func (s *serveServer) ownsDurableLinkedToolHandoff(ctx context.Context, deviceID, previous string, calls []string) bool {
	id, durable := parseDurableResponseMessageID(previous)
	store, supported := transcript.AsLinkedToolHandoffReader(s.store)
	if !durable || !supported {
		return false
	}
	pending, err := store.PendingLinkedToolHandoff(ctx, id, deviceID)
	return err == nil && wire.ExactToolCallIDs(pending, calls)
}
