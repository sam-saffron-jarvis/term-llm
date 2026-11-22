package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
)

// Typed WebMCP calls belong to the response that offered them, not to a live
// voice session. The response stream carries the request while an inline CLI
// provider waits; the page posts its answer back to this response only.
const typedClientToolTimeout = 120 * time.Second

// A linked tool offer belongs to the verified peer and the original POST.
// Keep the detached response alive, but never let another transport take over
// the offer or leave an inline provider waiting for a disappeared peer.
type linkedToolOrigin struct {
	deviceID string
	ctx      context.Context
	lifetime context.Context
}

func (o *linkedToolOrigin) accepts(r *http.Request) bool {
	peer, linked := linkeddevices.DeviceFromRequest(r)
	if o == nil {
		return !linked
	}
	return linked && peer.DeviceID == o.deviceID && peer.Lifetime != nil && peer.Lifetime.Err() == nil && o.ctx.Err() == nil && o.lifetime != nil && o.lifetime.Err() == nil
}

type typedClientToolRunner struct {
	origin  *linkedToolOrigin // nil for ordinary owner runs
	run     *responseRun
	runtime *serveRuntime
	names   map[string]bool
	specs   []core.ToolSpec
	lease   *core.ClientToolLease // nil until a reliable host lifecycle is attached
	mu      sync.Mutex
	mailbox *core.ClientToolMailbox
}

func (r *typedClientToolRunner) OwnsClientTool(name string) bool { return r.names[name] }

func (r *typedClientToolRunner) ClientToolLease() *core.ClientToolLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lease
}

func (r *typedClientToolRunner) clientMailbox() *core.ClientToolMailbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mailbox == nil {
		r.mailbox = core.NewClientToolMailbox(r.names, liveClientToolInlineCallLimit, typedClientToolTimeout, r.publishClientTool)
	}
	return r.mailbox
}

func (r *typedClientToolRunner) publishClientTool(ctx context.Context, call core.ToolCall) error {
	payload := map[string]any{"call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)}
	if r.origin != nil || core.ClientToolCallLease(ctx) != nil {
		payload["foreground_only"] = true
	}
	r.run.mu.Lock()
	defer r.run.mu.Unlock()
	if r.run.status != "in_progress" || r.run.cancelRequested {
		return errors.New("originating response ended before client publication")
	}
	if err := core.BeginClientToolDispatch(ctx); err != nil {
		return err
	}
	return r.run.appendEventLocked("response.client_tool.requested", payload, false)
}

func (r *typedClientToolRunner) RunClientTool(ctx context.Context, call core.ToolCall) (core.ToolOutput, error) {
	if r.runtime != nil {
		defer r.runtime.pauseForInteractiveWait()()
	}
	if r.origin != nil {
		// Provider IDs may contain path separators forbidden by peer ingress.
		// The mailbox correlation ID is independent of the provider's call ID.
		call.ID = linkeddevices.ToolResultID(call.ID)
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		if err := r.origin.ctx.Err(); err != nil {
			return core.ToolOutput{}, err
		}
		stop := context.AfterFunc(r.origin.ctx, cancel)
		defer stop()
	}
	return r.clientMailbox().RunClientTool(ctx, call)
}

func (r *typedClientToolRunner) pendingSnapshot() []map[string]string {
	if r.origin != nil {
		return nil
	}
	return r.clientMailbox().RecoveryPending()
}

func (s *serveServer) typedClientToolRunner(runtime *serveRuntime, names map[string]bool, specs []core.ToolSpec, origin *linkedToolOrigin) func(string) core.ClientToolRunner {
	return func(responseID string) core.ClientToolRunner {
		run, ok := s.ensureResponseRuns().get(responseID)
		if !ok {
			return nil
		}
		runner := &typedClientToolRunner{origin: origin, run: run, runtime: runtime, names: names, specs: specs}
		run.mu.Lock()
		run.typedClientTools = runner
		run.mu.Unlock()
		return runner
	}
}

// typedForegroundClientToolRunner pins the ORIGINAL streaming POST, not any
// later replay subscriber. Returning/disconnecting that stream irreversibly
// revokes its leases even when the host response and a script survive it.
func (s *serveServer) typedForegroundClientToolRunner(ctx context.Context, runtime *serveRuntime, tools []core.ToolSpec, enabled bool) (context.Context, func(string) core.ClientToolRunner, func()) {
	names, specs := core.ClientToolSpecsWithPrefix(tools, "webmcp__")
	if !enabled || len(names) == 0 {
		return ctx, nil, func() {}
	}
	var origin *linkedToolOrigin
	end := func() {}
	if peer := linkedRuntimePrincipal(ctx); peer.DeviceID != "" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		origin = &linkedToolOrigin{deviceID: peer.DeviceID, ctx: ctx, lifetime: peer.Lifetime}
		end = cancel
		if peer.Lifetime == nil || peer.Lifetime.Err() != nil {
			cancel()
		} else {
			stop := context.AfterFunc(peer.Lifetime, cancel)
			end = func() { stop(); cancel() }
		}
	}
	factory := s.typedClientToolRunner(runtime, names, specs, origin)
	foreground := core.NewClientToolForeground(ctx, "typed_"+randomSuffix())
	return foreground.Context(ctx), func(responseID string) core.ClientToolRunner {
		runner, ok := factory(responseID).(*typedClientToolRunner)
		if !ok {
			return nil
		}
		runner.mu.Lock()
		runner.lease = foreground.Bind(responseID, runner.run.sessionID, runner.specs)
		runner.mu.Unlock()
		return runner
	}, func() { end(); foreground.Close() }
}

func (r *responseRun) revokeClientToolLeaseLocked(reason string) {
	if r.clientToolLease != nil {
		r.clientToolLease.Revoke(reason)
	}
}

func (s *serveServer) handleTypedClientToolResult(w http.ResponseWriter, r *http.Request, run *responseRun, callID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if err := requireJSONContentType(r); err != nil {
		writeOpenAIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", err.Error())
		return
	}
	var body struct {
		Output string `json:"output"`
	}
	if err := decodeJSONBody(r, &body); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if len(body.Output) > 512<<10 {
		writeOpenAIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "client tool output too large")
		return
	}
	run.mu.Lock()
	runner := run.typedClientTools
	run.mu.Unlock()
	if runner == nil || !runner.origin.accepts(r) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "client tool call not found")
		return
	}
	switch runner.clientMailbox().Answer(callID, body.Output) {
	case core.ClientToolAnswerAccepted:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case core.ClientToolAnswerDuplicate:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true})
	case core.ClientToolAnswerConflict:
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "client tool already answered differently")
	default:
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "client tool call not found")
	}
}

func (r *responseRun) writeForegroundSafeEvent(ctx context.Context, w io.Writer, event responseRunEvent) error {
	if event.Event == "response.client_tool.requested" {
		r.mu.Lock()
		lease := r.clientToolLease
		linked := r.linkedDeviceID != ""
		r.mu.Unlock()
		if !core.ClientToolEventDeliverable(ctx, event.Data, lease) {
			if !linked {
				return nil
			}
			// Preserve sequence continuity for other observers without sending a
			// runnable call (or its arguments) outside the originating stream.
			event.Data, _ = json.Marshal(map[string]any{
				"response_id": r.id, "run_epoch": r.runEpoch, "sequence_number": event.Sequence,
			})
		}
	}
	return writeStoredResponseEvent(w, event)
}
