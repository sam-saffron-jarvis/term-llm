package serve

import (
	"context"
	"errors"
	"fmt"
	"github.com/term-llm/term-llm/internal/app/runtime/mcpstart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/live"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/mcp"
	runpkg "github.com/term-llm/term-llm/internal/run"
	"github.com/term-llm/term-llm/internal/session"
)

func linkedPolicyRequest(method, path, body string, p linkeddevices.DevicePrincipal) *http.Request {
	return linkeddevices.WithDevicePrincipal(httptest.NewRequest(method, path, strings.NewReader(body)), p)
}

func policyTestPrincipal(id string) linkeddevices.DevicePrincipal {
	return linkeddevices.DevicePrincipal{DeviceID: id, Scope: linkeddevices.ScopeWebV1, ManageMCP: true, MCPGrantLifetime: context.Background()}
}

// These are non-started connection descriptions. All tests below exercise
// admission/state only: no MCP processes, network requests or live models.
func linkedPolicyConnections(t *testing.T) map[string]mcp.ServerConfig {
	t.Helper()
	connections := map[string]mcp.ServerConfig{
		"private":   {Type: "http", URL: "https://private.example/mcp"},
		"shared":    {Type: "http", URL: "https://shared.example/mcp", LinkedDevices: true},
		"installed": {Type: "http", URL: "https://installed.example/mcp", LinkedInstaller: "one"},
	}
	writeServeMCPConfig(t, connections)
	return connections
}

func TestLinkedResponsePreparationPreflight(t *testing.T) {
	linkedPolicyConnections(t)
	for _, selection := range []string{"private", "shared", "removed"} {
		t.Run(selection, func(t *testing.T) {
			store := newServeMCPTestStore(t)
			s, created := newServeMCPHandlerTestServer(t, store)
			const id = "policy-response"
			if err := store.Create(t.Context(), &session.Session{ID: id, MCP: selection}); err != nil {
				t.Fatal(err)
			}
			r := linkedPolicyRequest("POST", "/v1/responses", "", policyTestPrincipal("two"))
			w := httptest.NewRecorder()
			_, release, stopped := s.prepareResolvedResponseAdmission(w, r, r.Context(), resolvedResponsesRequest{}, nil, id, "", "")
			release()
			if stopped != (selection == "private") {
				t.Fatalf("admission stopped = %v, response = %d %s", stopped, w.Code, w.Body.String())
			}
			if stopped && w.Code != http.StatusForbidden {
				t.Fatalf("denied admission = %d %s", w.Code, w.Body.String())
			}
			if *created != nil {
				t.Fatal("preflight created a runtime")
			}
		})
	}
}

func TestLinkedRuntimeDefaultsPreflight(t *testing.T) {
	linkedPolicyConnections(t)
	root := t.TempDir()
	writeAgentCatalogTestAgent(t, root, "default-mcp", "name: default-mcp\nmcp:\n  - name: private\n")
	cfg := &config.Config{DefaultProvider: "debug", Agents: config.AgentsConfig{SearchPaths: []string{root}}}
	ctx := linkedPolicyRequest("POST", "/v1/responses", "", policyTestPrincipal("two")).Context()
	for _, tc := range []struct {
		name, selection, agent string
		restored               *SessionSettings
		denied                 bool
	}{
		{name: "private host default", selection: "private", denied: true},
		{name: "fresh agent default", agent: "default-mcp", denied: true},
		{name: "restored input settings", restored: &SessionSettings{MCP: "private"}, denied: true},
		{name: "shared host default", selection: "shared"},
		{name: "shared override of agent", selection: "shared", agent: "default-mcp"},
		{name: "removed selection", selection: "removed"},
		{name: "no selection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &cmdRunner{baseCfg: cfg, defaults: cmdRunnerOptions{MCP: tc.selection, RestoreSettings: tc.restored}}
			stopped := errors.New("stop after admission, before runtime preparation")
			check := linkedMCPPreparationCheck(ctx)
			called := false
			runner.defaults.CheckMCPSelection = func(selection *mcpstart.PreparedSelection) error {
				called = true
				if err := check(selection); err != nil {
					return err
				}
				return stopped
			}
			_, err := runner.prepare(ctx, runpkg.Request{AgentName: tc.agent, Platform: runpkg.PlatformWeb, DeferSession: true}, nil)
			if !called || errors.Is(err, stopped) == tc.denied {
				t.Fatalf("default admission = %v, checked = %v, denied = %v", err, called, tc.denied)
			}
			if tc.denied && !strings.Contains(err.Error(), "not shared") {
				t.Fatalf("unexpected policy error: %v", err)
			}
		})
	}
}

func TestLinkedRuntimeRestoreChecksSelectionUnderLock(t *testing.T) {
	connections := linkedPolicyConnections(t)
	store := newServeMCPTestStore(t)
	s, created := newServeMCPHandlerTestServer(t, store)
	if err := store.Create(t.Context(), &session.Session{ID: "restore-policy", MCP: "private"}); err != nil {
		t.Fatal(err)
	}
	rt := &serveRuntime{mcpManager: mcp.NewManagerWithConfig(&mcp.Config{Servers: connections})}
	ctx := linkedPolicyRequest("POST", "/v1/responses", "", policyTestPrincipal("two")).Context()
	if err := s.ensureRuntimeMCPForSession(ctx, "restore-policy", rt); err == nil || !strings.Contains(err.Error(), "not shared") {
		t.Fatalf("restore admission = %v", err)
	}
	if enabled := rt.mcpManager.EnabledServers(); len(enabled) != 0 || rt.mcpSetting != "" || *created != nil {
		t.Fatalf("denied restore changed runtime: enabled=%v setting=%q", enabled, rt.mcpSetting)
	}
}

func newLinkedActivePolicyRuntime(t *testing.T, selection string, connections map[string]mcp.ServerConfig) (*serveServer, *serveRuntime, *responseRun) {
	t.Helper()
	s := newTestServeServer()
	t.Cleanup(s.sessionMgr.Close)
	s.store = newServeMCPTestStore(t)
	const id = "active-policy"
	if err := s.store.Create(t.Context(), &session.Session{ID: id, MCP: selection}); err != nil {
		t.Fatal(err)
	}
	rt, err := s.sessionMgr.GetOrCreate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	rt.mcpManager = mcp.NewManagerWithConfig(&mcp.Config{Servers: connections})
	rt.mcpSetting = selection
	run := &responseRun{id: "active-policy-run", sessionID: id, runEpoch: 1}
	manager := s.ensureResponseRuns()
	manager.mu.Lock()
	manager.runs[run.id] = run
	manager.activeBySession[id] = run.id
	manager.mu.Unlock()
	rt.engine.BeginSteeringRunForTest(true)
	// Model the long-held turn lock. Controls must finish before it is released.
	rt.mu.Lock()
	state := &runtimeInterruptState{responseRun: run}
	rt.setActiveInterrupt(state)
	t.Cleanup(func() { rt.clearActiveInterrupt(state); rt.mu.Unlock() })
	return s, rt, run
}

func TestLinkedActiveSteeringPolicy(t *testing.T) {
	connections := linkedPolicyConnections(t)
	for _, tc := range []struct {
		selection, peer string
		denied          bool
	}{
		{"private", "two", true},
		{"shared", "two", false},
		{"installed", "one", false},
		{"installed", "two", true},
	} {
		t.Run(tc.selection+"/"+tc.peer, func(t *testing.T) {
			s, rt, run := newLinkedActivePolicyRuntime(t, tc.selection, connections)
			r := linkedPolicyRequest("POST", "/v1/live/sessions", "", policyTestPrincipal(tc.peer))
			call := newLiveSession("policy-call", run.sessionID)
			call.linkedOwner = linkeddevices.NewLiveOwner(r)
			d := &serveLiveDelegator{server: s, live: call}
			admitted, err := d.Steer(t.Context(), live.DelegationRequest{ID: "voice", Input: "prefer the concise summary"})
			if admitted == tc.denied || (err != nil) != tc.denied {
				t.Fatalf("Live admission = %v, %v", admitted, err)
			}
			// The typed HTTP endpoint reaches the same gate without taking rt.mu.
			body := fmt.Sprintf(`{"message":"include references","client_message_id":"typed","delivery":"steer","expected_response_id":%q,"expected_run_epoch":%d}`, run.id, run.runEpoch)
			typed := linkedPolicyRequest("POST", "/v1/sessions/"+run.sessionID+"/steering", body, policyTestPrincipal(tc.peer))
			w := httptest.NewRecorder()
			s.handleSessionInterrupt(w, typed, run.sessionID)
			wantStatus := http.StatusOK
			if tc.denied {
				wantStatus = http.StatusConflict
			}
			if w.Code != wantStatus {
				t.Fatalf("typed admission = %d %s", w.Code, w.Body.String())
			}
			want := 2
			if tc.denied {
				want = 0
			}
			if pending := rt.engine.ListPendingSteering(); len(pending) != want {
				t.Fatalf("pending steering = %+v", pending)
			}
		})
	}
}

func TestLinkedActiveGoalPolicy(t *testing.T) {
	connections := linkedPolicyConnections(t)
	for _, selection := range []string{"private", "shared", "installed"} {
		t.Run(selection, func(t *testing.T) {
			s, _, run := newLinkedActivePolicyRuntime(t, selection, connections)
			for _, peer := range []string{"one", "two"} {
				r := linkedPolicyRequest("PATCH", "/v1/sessions/"+run.sessionID+"/runtime/goal", `{"action":"set","objective":"summarize the plan"}`, policyTestPrincipal(peer))
				w := httptest.NewRecorder()
				s.handleSessionRuntimeGoal(w, r, run.sessionID)
				want := http.StatusOK
				if selection == "private" || (selection == "installed" && peer != "one") {
					want = http.StatusForbidden
				}
				if w.Code != want {
					t.Fatalf("%s goal = %d %s, want %d", peer, w.Code, w.Body.String(), want)
				}
			}
			sess, err := s.store.Get(t.Context(), run.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if (sess.Goal == nil) != (selection == "private") {
				t.Fatalf("persisted goal = %+v", sess.Goal)
			}
		})
	}
}

func TestLinkedActivePolicyDoesNotCachePrincipal(t *testing.T) {
	connections := linkedPolicyConnections(t)
	s, rt, run := newLinkedActivePolicyRuntime(t, "installed", connections)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			peer := "one"
			if i%2 != 0 {
				peer = "two"
			}
			r := linkedPolicyRequest("POST", "/steering", "", policyTestPrincipal(peer))
			_, _, err := rt.InterruptMessage(r.Context(), core.UserText("keep this focused"), "keep this focused", fmt.Sprintf("policy-%d", i), nil, interruptDeliverySteer)
			if (err != nil) != (peer == "two") {
				errs <- fmt.Errorf("%s steering: %v", peer, err)
			}
			// Goal policy uses the same snapshot without serializing on a turn.
			release, err := s.authorizeLinkedGoalEdit(r.Context(), run.sessionID)
			if err == nil {
				release()
			}
			// Concurrent goal editors may report busy, but cannot borrow another
			// principal's authority.
			if peer == "two" && err == nil {
				errs <- fmt.Errorf("%s goal unexpectedly authorized", peer)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if pending := rt.engine.ListPendingSteering(); len(pending) != 10 {
		t.Fatalf("queued = %d, want 10", len(pending))
	}
}

func TestLinkedPrivateTurnStillAllowsCancellation(t *testing.T) {
	connections := linkedPolicyConnections(t)
	_, rt, _ := newLinkedActivePolicyRuntime(t, "private", connections)
	cancelled := false
	rt.interruptMu.Lock()
	rt.activeInterrupt.cancel = func() { cancelled = true }
	rt.interruptMu.Unlock()
	r := linkedPolicyRequest("POST", "/interrupt", "", policyTestPrincipal("two"))
	action, _, err := rt.InterruptMessage(r.Context(), core.UserText("/stop"), "/stop", "stop", nil, interruptDeliveryAuto)
	if err != nil || action != engine.InterruptCancel || !cancelled {
		t.Fatalf("cancel = %v, %v, called=%v", action, err, cancelled)
	}
}

func TestLinkedLivePreparationPreflight(t *testing.T) {
	linkedPolicyConnections(t)
	store := newServeMCPTestStore(t)
	s, created := newServeMCPHandlerTestServer(t, store)
	const id = "live-preparation"
	if err := store.Create(t.Context(), &session.Session{ID: id, MCP: "private"}); err != nil {
		t.Fatal(err)
	}
	ctx := linkedPolicyRequest("POST", "/v1/live/sessions", "", policyTestPrincipal("two")).Context()
	if _, _, err := s.linkedRuntimeForRequest(ctx, id); err == nil {
		t.Fatal("private Live preparation authorized")
	}
	if *created != nil {
		t.Fatal("denied Live preparation warmed runtime")
	}
}

func TestLinkedIdleGoalUpdatesRuntimeMetadata(t *testing.T) {
	connections := linkedPolicyConnections(t)
	s := newTestServeServer()
	t.Cleanup(s.sessionMgr.Close)
	s.store = newServeMCPTestStore(t)
	const id = "idle-goal"
	sess := &session.Session{ID: id, MCP: "shared"}
	if err := s.store.Create(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	rt, err := s.sessionMgr.GetOrCreate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	rt.mcpManager = mcp.NewManagerWithConfig(&mcp.Config{Servers: connections})
	rt.mcpSetting, rt.sessionMeta = "shared", sess
	r := linkedPolicyRequest("PATCH", "/v1/sessions/"+id+"/runtime/goal", `{"objective":"summarize the plan"}`, policyTestPrincipal("two"))
	w := httptest.NewRecorder()
	s.handleSessionRuntimeGoal(w, r, id)
	if w.Code != http.StatusOK || rt.sessionMeta.Goal == nil || rt.sessionMeta.Goal.Objective != "summarize the plan" {
		t.Fatalf("goal = %d %s, metadata = %+v", w.Code, w.Body.String(), rt.sessionMeta.Goal)
	}
}
