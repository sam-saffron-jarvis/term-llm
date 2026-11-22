package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func TestLinkedGoalEditDuringTurnStartupReportsBusy(t *testing.T) {
	connections := linkedPolicyConnections(t)
	s, rt, run := newLinkedActivePolicyRuntime(t, "shared", connections)
	// Startup owns rt.mu but has not published a turn policy yet. Do not wait
	// for that turn, and do not infer access from a missing policy snapshot.
	rt.setActiveInterrupt(nil)
	r := linkedPolicyRequest("PATCH", "/v1/sessions/"+run.sessionID+"/runtime/goal", `{"objective":"summarize the plan"}`, policyTestPrincipal("two"))
	w := httptest.NewRecorder()
	s.handleSessionRuntimeGoal(w, r, run.sessionID)
	if w.Code != http.StatusConflict {
		t.Fatalf("startup goal = %d %s", w.Code, w.Body.String())
	}
	sess, err := s.store.Get(t.Context(), run.sessionID)
	if err != nil || sess.Goal != nil {
		t.Fatalf("startup changed goal: %+v, %v", sess, err)
	}
}

func TestLinkedActiveControlsRecheckManagementLifetime(t *testing.T) {
	connections := linkedPolicyConnections(t)
	s, rt, run := newLinkedActivePolicyRuntime(t, "installed", connections)
	lifetime, revoke := context.WithCancel(t.Context())
	defer revoke()
	principal := policyTestPrincipal("one")
	principal.MCPGrantLifetime = lifetime
	r := linkedPolicyRequest("POST", "/steering", "", principal)
	message := core.UserText("keep this focused")
	if _, _, err := rt.InterruptMessage(r.Context(), message, "keep this focused", "same-steering", nil, interruptDeliverySteer); err != nil {
		t.Fatal(err)
	}
	revoke()
	if _, _, err := rt.InterruptMessage(r.Context(), message, "keep this focused", "same-steering", nil, interruptDeliverySteer); err == nil {
		t.Fatal("steering retry retained revoked management authority")
	}
	if release, err := s.authorizeLinkedGoalEdit(r.Context(), run.sessionID); err == nil {
		release()
		t.Fatal("goal edit retained revoked management authority")
	}
	if pending := rt.engine.ListPendingSteering(); len(pending) != 1 {
		t.Fatalf("pending steering = %+v", pending)
	}
}
