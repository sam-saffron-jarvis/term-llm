package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestLinkedToolContinuationOwnership(t *testing.T) {
	const callID = "call_pending"
	srv := &serveServer{responseRuns: newServeResponseRunManager()}
	t.Cleanup(srv.responseRuns.Close)
	lifetime, revoke := context.WithCancel(t.Context())
	defer revoke()
	run := newResponseRun("resp_linked", "chat", "", "mock", 1, nil)
	run.linkedDeviceID = "phone"
	run.status = "completed"
	run.continuationResponseID = durableResponseIDForMessageID(42)
	run.pendingClientCalls = wire.PendingClientCallsPayload([]core.ToolCall{{ID: callID, Name: "webmcp__ping"}})
	if err := srv.responseRuns.create(run); err != nil {
		t.Fatal(err)
	}
	ownerRun := newResponseRun("resp_owner", "other-chat", "", "mock", 1, nil)
	ownerRun.status = "completed"
	ownerRun.pendingClientCalls = run.pendingClientCalls
	if err := srv.responseRuns.create(ownerRun); err != nil {
		t.Fatal(err)
	}
	post := func(device, cursor, id string, want bool) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		if device != "" {
			r = linkedLiveRequest(r.Method, r.URL.Path, "", device, lifetime)
		}
		if got := srv.allowLinkedToolContinuation(w, r, cursor, []core.Message{core.ToolResultMessage(id, "", "pong", nil)}); got != want || !want && w.Code != http.StatusNotFound {
			t.Fatalf("device=%q cursor=%q call=%q: allowed=%t status=%d", device, cursor, id, got, w.Code)
		}
	}
	post("phone", run.id, callID, true)
	post("phone", run.continuationResponseID, callID, true)
	post("phone", run.id, "unknown", false)
	post("phone", ownerRun.id, callID, false)
	post("another", run.continuationResponseID, callID, false)
	post("", ownerRun.id, callID, true)
	revoke()
	post("phone", run.id, callID, false)
}
