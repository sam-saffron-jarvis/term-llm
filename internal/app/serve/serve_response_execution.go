package serve

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session"
)

func (s *serveServer) executeSynchronousResponse(ctx context.Context, w http.ResponseWriter, runtime *serveRuntime, stateful, replaceHistory bool, inputMessages []core.Message, llmReq core.Request, sessionID, previousResponseID string, modelSwapExec *responseModelSwapExecution, resetResponseIDsOnSuccess bool) {
	if stateful && s.sessionMgr != nil {
		var retained *serveRuntime
		if modelSwapExec != nil {
			retained = modelSwapExec.previous
		}
		releaseAdmission, admissionErr := s.sessionMgr.admitSynchronousActivity(sessionID, runtime, retained)
		if admissionErr != nil {
			writeOpenAIError(w, http.StatusConflict, "conflict_error", admissionErr.Error())
			return
		}
		defer releaseAdmission()
	}
	// Synchronous API invocations do not acquire an OS destination, but an
	// admitted human must still retire prior held candidates. Runtime setup runs
	// only after tryLockRun succeeds, never on busy/rejected handler paths.
	if turn := s.notificationTurnAdmission(ctx, inputMessages, previousResponseID); turn != nil && turn.Human {
		if turns, ok := session.AsNotificationTurnStore(s.store); ok {
			previousSetup := serveRuntimeSetupFromContext(ctx)
			var once sync.Once
			var supersedeErr error
			ctx = withServeRuntimeSetup(ctx, func(req *core.Request) error {
				if previousSetup != nil {
					if err := previousSetup(req); err != nil {
						return err
					}
				}
				once.Do(func() { supersedeErr = turns.SupersedeSessionNotificationTurn(ctx, sessionID) })
				return supersedeErr
			})
		}
	}
	result, _, err := s.runResponseWithModelSwapFallback(ctx, runtime, stateful, replaceHistory, inputMessages, llmReq, sessionID, modelSwapExec)
	if err != nil {
		if errors.Is(err, errServeSessionBusy) {
			writeOpenAIError(w, http.StatusConflict, "conflict_error", err.Error())
			return
		}
		if errors.Is(err, errServeSessionPersistence) {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", errServeSessionPersistence.Error())
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			writeOpenAIError(w, http.StatusRequestTimeout, "timeout_error", responseRunTimeoutMessage(s.responseTimeout()))
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if s.cfg.suppressServerTools {
		filtered := make([]core.ToolCall, 0, len(result.ToolCalls))
		for _, call := range result.ToolCalls {
			if !runtime.isServerExecutedTool(call.Name) {
				filtered = append(filtered, call)
			}
		}
		result.ToolCalls = filtered
	}

	model := llmReq.Model
	if model == "" {
		model = runtime.defaultModel
	}

	setSessionNumberHeader(w, runtime)

	created := time.Now().Unix()
	respID, err := s.storeCompletedResponseRun(ctx, runtime, sessionID, previousResponseID, model, created, result, resetResponseIDsOnSuccess)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	s.scheduleAutoTitle(sessionID, runtime.providerKey)

	writeJSON(w, http.StatusOK, responsesFinalResponse(result, model, respID, created))
}

func (s *serveServer) storeCompletedResponseRun(ctx context.Context, runtime *serveRuntime, sessionID, previousResponseID, model string, created int64, result serveRunResult, resetResponseIDsOnSuccess bool) (string, error) {
	mgr := s.ensureResponseRuns()

	respID := "resp_" + randomSuffix()
	run := newResponseRun(respID, sessionID, previousResponseID, model, created, nil)
	run.linkedDeviceID = linkedRuntimePrincipal(ctx).DeviceID
	s.configureResponseRunRevision(run, sessionID)
	if err := mgr.create(run); err != nil {
		return "", err
	}

	cleanup := func() {
		mgr.delete(respID)
	}
	createdResponse := map[string]any{
		"id":      respID,
		"object":  "response",
		"created": created,
		"model":   model,
		"status":  "in_progress",
	}
	if err := run.appendEvent("response.created", map[string]any{
		"response": createdResponse,
	}); err != nil {
		cleanup()
		return "", err
	}
	if result.Text.Len() > 0 {
		if err := run.appendTextDeltaSegmentEvent(0, 0, result.Text.String()); err != nil {
			cleanup()
			return "", err
		}
	}
	durableID := s.latestDurableResponseIDForSessionBestEffort(context.Background(), sessionID)
	completedID := respID
	if durableID != "" {
		completedID = durableID
	}
	completedResponse := map[string]any{
		"id":            completedID,
		"object":        "response",
		"created":       created,
		"model":         model,
		"status":        "completed",
		"usage":         usagePayload(result.Usage),
		"session_usage": usagePayload(result.SessionUsage),
		"context_usage": result.ContextUsage,
		// The client tool calls this response stopped on; authoritative even when empty.
		"pending_client_calls": wire.PendingClientCallsPayload(result.PendingClientCalls()),
	}
	if err := run.complete(map[string]any{
		"response": completedResponse,
	}, result.Usage, result.SessionUsage); err != nil {
		cleanup()
		return "", err
	}

	mgr.scheduleCleanup(respID)
	if resetResponseIDsOnSuccess {
		s.unregisterSessionResponseIDs(sessionID)
	}
	if completedID != respID {
		s.registerResponseID(runtime, respID, sessionID)
	}
	s.registerResponseID(runtime, completedID, sessionID)
	return completedID, nil
}
