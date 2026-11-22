package serve

import (
	"context"
	"errors"
	"strings"

	"github.com/term-llm/term-llm/internal/live"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session"
)

// Steer admits a voice follow-up through the same durable, run-fenced path as
// typed steering. It never cancels a task or subscribes to its output again.
// The controller calls this directly, outside Run, so the binding is snapshotted
// here rather than by the caller.
func (d *serveLiveDelegator) Steer(ctx context.Context, request live.DelegationRequest) (bool, error) {
	sessionID := ""
	if d.live != nil {
		sessionID = d.live.boundSession()
	}
	return d.steerSession(ctx, sessionID, request)
}

// steerSession steers the task running in sessionID, which the caller resolved
// from the binding once.
func (d *serveLiveDelegator) steerSession(ctx context.Context, sessionID string, request live.DelegationRequest) (bool, error) {
	if strings.TrimSpace(request.Input) == "" {
		return false, errors.New("the voice delegation contained no request")
	}
	s := d.server
	if s == nil || s.sessionMgr == nil || sessionID == "" {
		return false, errors.New("the session runtime is unavailable")
	}
	manager := s.ensureResponseRuns()
	run := manager.activeRun(sessionID)
	if run == nil {
		return false, nil
	}
	runtime, ok := s.sessionMgr.Get(sessionID)
	if !ok {
		return false, nil
	}
	// A run is registered before its engine is ready for steering. Let the
	// controller retry rather than discard guidance in that startup window.
	runtime.interruptMu.Lock()
	ready := runtime.activeInterrupt != nil && runtime.activeInterrupt.responseRun == run
	runtime.interruptMu.Unlock()
	if !ready {
		return false, nil
	}
	id := strings.TrimSpace(request.ID)
	if id == "" {
		id = session.NewID()
	}
	if d.live != nil {
		id = d.live.id + ":" + id
	}
	id = "live-steering:" + id
	message := core.UserText(live.DelegationPrompt(request.Input, request.TranscriptDelta))
	message.DisplayText = strings.TrimSpace(request.Input)
	var admissionErr error
	owned := manager.withExpectedActiveRun(sessionID, run.id, run.runEpoch, func() {
		_, _, admissionErr = runtime.InterruptMessage(linkedLiveContext(ctx, d.live), message, message.DisplayText, id, nil, interruptDeliverySteer)
	})
	if !owned {
		return false, nil
	}
	if errors.Is(admissionErr, errSteeringRunFinished) {
		return false, nil
	}
	if admissionErr != nil {
		return false, admissionErr
	}
	if d.live != nil {
		d.live.touch()
	}
	return true, nil
}
