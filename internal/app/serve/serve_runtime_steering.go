package serve

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
)

func (rt *serveRuntime) InterruptMessage(ctx context.Context, msg core.Message, displayText string, steeringID string, fastProvider core.Provider, delivery interruptDelivery, origins ...engine.SteeringOrigin) (engine.InterruptAction, bool, error) {
	steeringID = strings.TrimSpace(steeringID)
	if delivery == "" {
		delivery = interruptDeliveryAuto
	}
	if delivery != interruptDeliveryAuto && delivery != interruptDeliverySteer {
		return engine.InterruptSteer, false, fmt.Errorf("unsupported interrupt delivery %q", delivery)
	}
	if steeringID != "" && msg.Role == core.RoleUser {
		msg.ClientMessageID = steeringID
	}
	fingerprint := ""
	if steeringID != "" {
		var err error
		fingerprint, err = steeringFingerprint(msg, displayText, delivery)
		if err != nil {
			return engine.InterruptSteer, false, err
		}
	}

	classifyText := strings.TrimSpace(displayText)
	if classifyText == "" {
		classifyText = strings.TrimSpace(core.MessageText(msg))
	}
	if summary := core.MessageAttachmentSummary(msg); summary != "" {
		if classifyText != "" {
			classifyText += " "
		}
		classifyText += summary
	}

	rt.interruptMu.Lock()
	// Cancellation remains a shared control; guidance uses the turn's immutable
	// selection, never mutable runtime fields protected by the long-held run lock.
	cancelOnly := false
	if delivery == interruptDeliveryAuto && core.MessageAttachmentSummary(msg) == "" {
		action, immediate := engine.ClassifyInterruptImmediate(classifyText)
		cancelOnly = immediate && action == engine.InterruptCancel
	}
	if !cancelOnly && rt.activeInterrupt != nil {
		if err := checkLinkedMCPPolicy(ctx, rt.activeInterrupt.mcpPolicy); err != nil {
			rt.interruptMu.Unlock()
			return engine.InterruptSteer, false, err
		}
	}
	now := time.Now()
	for id, existing := range rt.steeringCalls {
		if !existing.completedAt.IsZero() && now.Sub(existing.completedAt) > runtimeSteeringCallTTL {
			delete(rt.steeringCalls, id)
		}
	}
	var call *runtimeSteeringCall
	if steeringID != "" {
		if rt.steeringCalls == nil {
			rt.steeringCalls = make(map[string]*runtimeSteeringCall)
		}
		if existing := rt.steeringCalls[steeringID]; existing != nil {
			if existing.fingerprint != fingerprint {
				rt.interruptMu.Unlock()
				return engine.InterruptSteer, false, fmt.Errorf("steering id %q was already used for different content", steeringID)
			}
			rt.interruptMu.Unlock()
			select {
			case <-existing.done:
				return existing.action, true, existing.err
			case <-ctx.Done():
				return engine.InterruptSteer, true, ctx.Err()
			}
		}
		call = &runtimeSteeringCall{done: make(chan struct{}), fingerprint: fingerprint}
		rt.steeringCalls[steeringID] = call
	}
	state := rt.activeInterrupt
	if state == nil {
		if call != nil {
			delete(rt.steeringCalls, steeringID)
		}
		rt.interruptMu.Unlock()
		return engine.InterruptSteer, false, fmt.Errorf("session has no active stream: %w", errSteeringRunFinished)
	}
	cancel := state.cancel
	requestCancel := state.requestCancel
	persistPendingSteering := state.persistPendingSteering
	removePendingSteering := state.removePendingSteering
	activity := engine.InterruptActivity{
		CurrentTask: state.currentTask,
		ToolsRun:    append([]string(nil), state.toolsRun...),
		ProseLen:    state.proseLen,
		ActiveTool:  state.activeTool,
	}
	rt.interruptMu.Unlock()
	action := engine.InterruptSteer
	if delivery == interruptDeliveryAuto {
		classifyCtx := ctx
		classifyCancel := func() {}
		if steeringID != "" {
			// Stay below the web client's 5-second mutation first-frame timeout so a
			// healthy classifier normally responds before transport fallback begins.
			classifyCtx, classifyCancel = context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
		}
		action = engine.ClassifyInterrupt(classifyCtx, fastProvider, classifyText, activity)
		classifyCancel()
	}
	var resultErr error
	switch action {
	case engine.InterruptCancel:
		rt.steeringMutationMu.Lock()
		var discarded []engine.QueuedSteering
		if rt.engine != nil {
			discarded = rt.engine.ListPendingSteering()
			rt.engine.DiscardPendingSteering()
		}
		if removePendingSteering != nil {
			for _, entry := range discarded {
				removePendingSteering(context.WithoutCancel(ctx), entry.ID)
			}
		}
		rt.steeringMutationMu.Unlock()
		if requestCancel != nil {
			requestCancel()
		} else if cancel != nil {
			cancel()
		}
	case engine.InterruptSteer:
		entry := engine.QueuedSteering{ID: steeringID, Message: msg, DisplayText: displayText, Origin: engine.SteeringOriginForMessage(msg)}
		if len(origins) > 0 {
			entry.Origin = origins[0]
		}
		rt.steeringMutationMu.Lock()
		if rt.engine.SteeringTransitioning() {
			resultErr = engine.ErrSteeringTransition
			rt.steeringMutationMu.Unlock()
			break
		}
		if persistPendingSteering != nil {
			if err := persistPendingSteering(ctx, entry); err != nil {
				resultErr = fmt.Errorf("persist pending steering: %w", err)
				rt.steeringMutationMu.Unlock()
				break
			}
		}
		rt.grantUploadedFileReads([]core.Message{msg})
		_, queueStatus := rt.engine.QueueSteeringWithStatus(entry)
		switch queueStatus {
		case engine.SteeringQueueTransitioning, engine.SteeringQueueRushOwned, engine.SteeringQueueFollowUpOwned, engine.SteeringQueueCommitted:
			resultErr = fmt.Errorf("steering %q is already %s", steeringID, queueStatus)
		case engine.SteeringQueueRunFinished:
			resultErr = fmt.Errorf("active run finished before steering %q could be consumed: %w", steeringID, errSteeringRunFinished)
		}
		if resultErr != nil && removePendingSteering != nil {
			removePendingSteering(context.WithoutCancel(ctx), steeringID)
		}
		state.publishSteeringAdmission(entry, resultErr)
		rt.steeringMutationMu.Unlock()
	}
	rt.completeSteeringCall(steeringID, call, action, resultErr)
	return action, false, resultErr
}
