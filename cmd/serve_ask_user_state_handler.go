package cmd

import "net/http"

func (s *serveServer) handleSessionState(w http.ResponseWriter, r *http.Request, sessionID string) {
	resp := map[string]any{
		"active_run": false,
	}

	if s.sessionMgr != nil {
		if rt, ok := s.sessionMgr.Get(sessionID); ok && rt != nil {
			activeRun := rt.hasActiveRun()
			resp["active_run"] = activeRun
			if prompts := rt.pendingAskUserPrompts(); len(prompts) > 0 {
				resp["pending_ask_users"] = prompts
				resp["pending_ask_user"] = prompts[0]
			}
			if approvals := rt.pendingApprovalPrompts(); len(approvals) > 0 {
				resp["pending_approvals"] = approvals
				resp["pending_approval"] = approvals[0]
			}
			if !activeRun {
				if lastErr := rt.consumeLastUIRunError(); lastErr != "" {
					resp["last_error"] = lastErr
				}
			}
		}
	}
	if s.responseRuns != nil {
		if activeResponseID := s.responseRuns.activeRunID(sessionID); activeResponseID != "" {
			// Detached response runs outlive the browser request, so they are the
			// durable source of truth for reload/reconnect even if runtime polling
			// has not observed the active run yet.
			resp["active_run"] = true
			resp["active_response_id"] = activeResponseID
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
