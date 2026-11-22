package serve

import (
	"context"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/tools/agentwait"
)

func withNotificationWaitObserver(ctx context.Context, run *responseRun) context.Context {
	return agentwait.WithObserver(ctx, func(child session.AgentRun) {
		if child.ParentSessionID != run.sessionID || child.ParentResponseID == "" {
			return
		}
		run.mu.Lock()
		defer run.mu.Unlock()
		for i, previous := range run.awaitedEvents {
			if previous.ID == child.ID {
				run.awaitedEvents[i] = child
				return
			}
		}
		run.awaitedEvents = append(run.awaitedEvents, child)
	})
}

// Called under run.mu by the existing fenced lifecycle finalizer.
func notificationTerminal(run *responseRun, outcome session.ResponseRunState, finalRev int64, count int, owner string, fence int64) session.ResponseRunTerminal {
	anchor, _ := parseDurableResponseMessageID(run.continuationResponseID)
	return session.ResponseRunTerminal{LinkedDeviceID: run.linkedDeviceID, LinkedToolHandoffMessageID: anchor, ResponseID: run.id, OwnerInstanceID: owner, FencingToken: fence, Outcome: outcome, FinalRev: finalRev, DurableOutputCount: count, EndedAt: time.Now().UTC(), PendingClientCalls: len(run.pendingClientCalls) > 0, PendingClientCallIDs: wire.PendingClientCallIDs(run.pendingClientCalls), AwaitedEvents: run.awaitedEvents, AwaitingAgentJoin: len(run.awaitedEvents) > 0, Recoverable: run.recoverableNotification, AgentEvents: run.agentEvents}
}
