package serve

import (
	"context"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session/notificationdb"
	"github.com/term-llm/term-llm/internal/session/transcript"
)

type linkedToolClaimKey struct{}

func wantsLinkedToolReceipt(r *http.Request, req responsesCreateRequest) bool {
	_, linked := linkeddevices.DeviceFromRequest(r)
	return linked && req.PreviousResponseID != "" && strings.TrimSpace(req.ClientMessageID) != ""
}

// An exact retry may replay its own accepted request even when its predecessor
// is no longer pending. New requests still have to prove current ownership.
func (s *serveServer) prepareLinkedToolContinuation(w http.ResponseWriter, r *http.Request, ctx context.Context, req responsesCreateRequest, input []core.Message, admission responsesSessionAdmission, replayOnly bool) (context.Context, bool) {
	peer, linked := linkeddevices.DeviceFromRequest(r)
	calls := wire.ToolResultIDs(input)
	if !linked || len(calls) == 0 {
		return ctx, false
	}
	if req.Branch || strings.TrimSpace(req.PreviousResponseID) == "" || peer.Lifetime == nil || peer.Lifetime.Err() != nil {
		writeLinkedToolHandoffNotFound(w)
		return ctx, true
	}
	key := strings.TrimSpace(req.ClientMessageID)
	if req.Stream && key != "" && admission.requestFingerprint != "" {
		if s.replayLinkedToolContinuation(w, ctx, peer, req.PreviousResponseID, key, admission, replayOnly) {
			return ctx, true
		}
		ctx = context.WithValue(ctx, linkedToolClaimKey{}, &notificationdb.LinkedToolClaim{DeviceID: peer.DeviceID, Key: key, Fingerprint: admission.requestFingerprint})
	}
	if replayOnly {
		writeResponseAdmissionInstanceChanged(w)
		return ctx, true
	}
	if !s.ownsLinkedToolHandoff(ctx, peer.DeviceID, strings.TrimSpace(req.PreviousResponseID), calls) || peer.Lifetime.Err() != nil {
		writeLinkedToolHandoffNotFound(w)
		return ctx, true
	}
	return ctx, false
}

func writeLinkedToolHandoffNotFound(w http.ResponseWriter) {
	writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "client tool handoff not found")
}

func (s *serveServer) replayLinkedToolContinuation(w http.ResponseWriter, ctx context.Context, peer linkeddevices.DevicePrincipal, previous, key string, admission responsesSessionAdmission, durableOnly bool) bool {
	sessionID := admission.headerSessionID
	if id, durable := parseDurableResponseMessageID(previous); durable && s.store != nil {
		// Resolve identity only; staleness is checked for new work below.
		msg, err := getMessageByID(ctx, s.store, id)
		if err == nil && msg.SessionID != "" {
			if sessionID != "" && sessionID != msg.SessionID {
				return false
			}
			sessionID = msg.SessionID
		}
	} else if sessionID == "" {
		if mapped, ok := s.responseToSession.Load(previous); ok {
			sessionID, _ = mapped.(string)
		}
	}
	if sessionID == "" {
		return false
	}
	receipt, err := transcript.ReadLinkedToolReceipt(ctx, s.store, sessionID, peer.DeviceID, key)
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "could not check client tool handoff receipt")
		return true
	}
	if peer.Lifetime.Err() != nil {
		writeLinkedToolHandoffNotFound(w)
		return true
	}
	mgr := s.ensureResponseRuns()
	if receipt.ResponseID != "" {
		if receipt.Fingerprint != admission.requestFingerprint {
			writeOpenAIError(w, http.StatusConflict, "conflict_error", errResponseRunKeyConflict.Error())
		} else if run, found := mgr.get(receipt.ResponseID); found && run.linkedDeviceID == peer.DeviceID {
			s.replayLinkedToolRun(ctx, w, run)
		} else {
			writeLinkedToolReceipt(w, receipt.State)
		}
		return true
	}
	if durableOnly {
		return false
	}
	// Stores without durable receipts can still replay the exact in-memory
	// transport claim. Do not reveal another principal's claim or fingerprint.
	run, found, _ := mgr.getByIdempotencyClaim(sessionID, admission.runIdempotencyKey, admission.requestFingerprint)
	if found && run.linkedDeviceID == peer.DeviceID && run.requestFingerprint == admission.requestFingerprint && run.hasReplayableAdmission() {
		s.replayLinkedToolRun(ctx, w, run)
		return true
	}
	return false
}

func (s *serveServer) replayLinkedToolRun(ctx context.Context, w http.ResponseWriter, run *responseRun) {
	w.Header().Set("x-session-id", run.sessionID)
	w.Header().Set("x-response-id", run.id)
	s.setReplaySessionNumberHeader(ctx, w, run.sessionID)
	s.streamResponseRunEvents(ctx, w, run, 0)
}

func writeLinkedToolReceipt(w http.ResponseWriter, state notificationdb.ResponseRunState) {
	switch state {
	case notificationdb.ResponseRunCompleted:
		writeOpenAIError(w, http.StatusConflict, "client_message_already_committed", "client tool results were already applied; refresh the conversation")
	case "running":
		writeOpenAIError(w, http.StatusTooEarly, "client_tool_handoff_in_progress", "client tool handoff was already admitted and is still running")
	default:
		writeOpenAIError(w, http.StatusConflict, "client_tool_handoff_ended", "client tool handoff was already admitted but did not complete")
	}
}

func (s *serveServer) ownsLinkedToolHandoff(ctx context.Context, deviceID, previous string, calls []string) bool {
	anchor, matched := s.ensureResponseRuns().linkedToolHandoffAnchor(deviceID, previous, calls)
	if !matched {
		anchor = previous
	}
	store, supported := transcript.AsLinkedToolHandoffReader(s.store)
	if !supported {
		return matched
	}
	id, durable := parseDurableResponseMessageID(anchor)
	if !durable {
		return false
	}
	pending, err := store.PendingLinkedToolHandoff(ctx, id, deviceID)
	return err == nil && wire.ExactToolCallIDs(pending, calls)
}

func (m *responseRunManager) linkedToolHandoffAnchor(deviceID, previous string, calls []string) (string, bool) {
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
		if run.linkedDeviceID == deviceID {
			if anchor, matches := run.matchesToolHandoff(previous, calls); matches {
				return anchor, true
			}
		}
	}
	return "", false
}

func (r *responseRun) matchesToolHandoff(previous string, calls []string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.continuationResponseID, r.status == "completed" && (previous == r.id || previous == r.continuationResponseID) &&
		wire.ExactToolCallIDs(wire.PendingClientCallIDs(r.pendingClientCalls), calls)
}

func (r *responseRun) hasReplayableAdmission() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSequenceNumber > 0
}
