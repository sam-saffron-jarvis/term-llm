package serve

import (
	"errors"
	"net/http"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/session"
)

func (s *serveServer) handleSessionRuntimeGoal(w http.ResponseWriter, r *http.Request, sessionID string) {
	if err := s.checkLinkedSessionMCP(r.Context(), sessionID); err != nil {
		writeOpenAIError(w, http.StatusForbidden, "permission_error", err.Error())
		return
	}
	var req sessionRuntimeGoalRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if s.store == nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session history is unavailable")
		return
	}
	sess, err := s.store.Get(r.Context(), sessionID)
	if (err != nil || sess == nil) && s.sessionMgr != nil {
		if rt, rtErr := s.metadataRuntime(r.Context(), sessionID); rtErr == nil && rt != nil {
			if !rt.mu.TryLock() {
				writeOpenAIError(w, http.StatusConflict, "conflict_error", "session is busy; retry after the active response finishes")
				return
			}
			if rt.ensurePersistedSession(r.Context(), sessionID, nil) && rt.sessionMeta != nil {
				sess = rt.sessionMeta
				err = nil
			}
			rt.mu.Unlock()
		}
	}
	if err != nil || sess == nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session not found")
		return
	}
	goal, err := wire.EditGoal(sess.Goal, req.Action, req.Objective, req.TokenBudget, time.Now())
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	release, err := s.authorizeLinkedGoalEdit(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, errServeSessionBusy) {
			writeOpenAIError(w, http.StatusConflict, "conflict_error", err.Error())
		} else {
			writeOpenAIError(w, http.StatusForbidden, "permission_error", err.Error())
		}
		return
	}
	if err := session.UpdateGoal(r.Context(), s.store, sessionID, goal); err != nil {
		release()
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to update goal")
		return
	}
	release()
	if s.sessionMgr != nil {
		if rt, ok := s.sessionMgr.Get(sessionID); ok && rt != nil {
			if rt.mu.TryLock() {
				if rt.sessionMeta != nil {
					rt.sessionMeta.Goal = goal.Clone()
				}
				rt.mu.Unlock()
			}
		}
	}
	s.publishEvent(serveEventInput{Type: serveEventSessionRuntimeChanged, SessionID: sessionID, Reason: "goal"})
	writeJSON(w, http.StatusOK, map[string]any{"goal": goal})
}
