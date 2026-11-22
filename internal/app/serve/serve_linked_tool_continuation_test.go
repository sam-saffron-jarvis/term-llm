package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/session"
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
		_, stop := srv.prepareLinkedToolContinuation(w, r, r.Context(), responsesCreateRequest{PreviousResponseID: cursor}, []core.Message{core.ToolResultMessage(id, "", "pong", nil)}, responsesSessionAdmission{}, false)
		if got := !stop; got != want || !want && w.Code != http.StatusNotFound {
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

func TestLinkedToolContinuationAfterEvictionAndRestart(t *testing.T) {
	for _, mode := range []string{"retained", "evicted", "reopened", "owner-origin"} {
		t.Run(mode, func(t *testing.T) {
			provider := mockprovider.NewMockProvider("debug").
				AddTurn(mockprovider.MockTurn{ToolCalls: []core.ToolCall{pendingTestCall("call-alpha", "webmcp__ping"), pendingTestCall("call-beta", "webmcp__ping")}}).
				AddTextResponse("incorporated linked results")
			path := filepath.Join(t.TempDir(), "sessions.db")
			newHost := func(createSession bool) *serveServer {
				store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				if createSession {
					if err := store.Create(t.Context(), &session.Session{ID: "wake-parent", Provider: "debug", ProviderKey: "debug", Model: "fast", Origin: session.OriginWeb, Status: session.StatusActive}); err != nil {
						t.Fatal(err)
					}
				}
				factory := func(context.Context, serveRuntimeRequest) (*serveRuntime, error) {
					return &serveRuntime{provider: provider, providerKey: "debug", engine: engine.NewEngine(provider, nil), store: store, defaultModel: "fast", platform: "web"}, nil
				}
				mgr := newServeSessionManager(time.Minute, 10, func(ctx context.Context) (*serveRuntime, error) { return factory(ctx, serveRuntimeRequest{}) })
				host := &serveServer{cfg: serveServerConfig{ui: true}, cfgRef: &config.Config{DefaultProvider: "debug", Providers: map[string]config.ProviderConfig{"debug": {Model: "fast"}}}, store: session.NewLoggingStore(store, nil), runtimeFactory: factory, sessionMgr: mgr, responseRuns: newServeResponseRunManager(), shutdownCh: make(chan struct{})}
				host.responseLifecycleOnce.Do(func() {})
				t.Cleanup(func() {
					close(host.shutdownCh)
					host.responseRuns.Close()
					host.agentWakeWG.Wait()
					mgr.Close()
				})
				return host
			}
			srv := newHost(true)
			const tools = `"tools":[{"type":"function","name":"webmcp__ping","parameters":{"type":"object"}}]`
			post := func(host *serveServer, device, body string, lifetime context.Context, instance ...string) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				if device != "" {
					r = linkedLiveRequest(r.Method, r.URL.Path, body, device, lifetime)
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Term-LLM-UI-Version", "test")
				r.Header.Set(requestSessionIDHeader, "wake-parent")
				var request struct {
					ClientMessageID string `json:"client_message_id"`
				}
				if err := json.Unmarshal([]byte(body), &request); err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Idempotency-Key", "key-"+request.ClientMessageID)
				if len(instance) > 0 {
					r.Header.Set(responseServerInstanceHeader, instance[0])
				}
				w := httptest.NewRecorder()
				host.handleResponses(w, r)
				return w
			}
			origin := "phone"
			if mode == "owner-origin" {
				origin = ""
			}
			first := post(srv, origin, `{"input":"ping","stream":true,"client_message_id":"linked-first",`+tools+`}`, t.Context())
			if first.Code != http.StatusOK {
				t.Fatalf("first response: %d %s", first.Code, first.Body)
			}
			run := waitNotificationNativeRun(t, srv, first.Header().Get("x-response-id"))
			cursor, _ := run.snapshot()["continuation_response_id"].(string)
			if _, ok := parseDurableResponseMessageID(cursor); !ok {
				t.Fatalf("missing durable cursor: %q", cursor)
			}
			<-run.settled
			if mode != "retained" {
				srv.responseRuns.delete(run.id)
			}
			if mode == "reopened" {
				// A separate SQLite connection and an empty response manager/map:
				// no handoff authority survives in the new host's memory.
				srv = newHost(false)
			}
			revoked, revoke := context.WithCancel(t.Context())
			revoke()
			body := func(key string, branch bool, ids ...string) string {
				input := make([]map[string]string, 0, len(ids))
				for _, id := range ids {
					input = append(input, map[string]string{"type": "function_call_output", "call_id": id, "output": "linked pong"})
				}
				data, _ := json.Marshal(input)
				return fmt.Sprintf(`{"stream":true,"branch":%t,"previous_response_id":%q,"client_message_id":%q,"input":%s,%s}`, branch, cursor, key, data, tools)
			}
			for _, tc := range []struct {
				name, device string
				lifetime     context.Context
				branch       bool
				ids          []string
			}{
				{"foreign", "other-phone", t.Context(), false, []string{"call-alpha", "call-beta"}},
				{"unknown", "phone", t.Context(), false, []string{"call-alpha", "not-pending"}},
				{"subset", "phone", t.Context(), false, []string{"call-alpha"}},
				{"duplicate", "phone", t.Context(), false, []string{"call-alpha", "call-alpha"}},
				{"revoked", "phone", revoked, false, []string{"call-alpha", "call-beta"}},
				{"branch", "phone", t.Context(), true, []string{"call-alpha", "call-beta"}},
			} {
				w := post(srv, tc.device, body(tc.name, tc.branch, tc.ids...), tc.lifetime)
				if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "client tool handoff not found") {
					t.Fatalf("%s handoff: %d %s", tc.name, w.Code, w.Body)
				}
			}
			if mode == "owner-origin" {
				if w := post(srv, "phone", body("owner-run", false, "call-alpha", "call-beta"), t.Context()); w.Code != http.StatusNotFound {
					t.Fatalf("linked peer answered an owner handoff: %d %s", w.Code, w.Body)
				}
			}
			accepted := post(srv, origin, body("exact", false, "call-beta", "call-alpha"), t.Context())
			if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), "event: response.completed") {
				t.Fatalf("owner-device handoff: %d %s", accepted.Code, accepted.Body)
			}
			requests := provider.RecordedRequests()
			if len(requests) != 2 {
				t.Fatalf("provider received %d requests, want 2", len(requests))
			}
			results := map[string]bool{}
			for _, msg := range requests[1].Messages {
				for _, part := range msg.Parts {
					if part.ToolResult != nil && strings.Contains(part.ToolResult.Content, "linked pong") {
						results[part.ToolResult.ID] = true
					}
				}
			}
			if !results["call-alpha"] || !results["call-beta"] {
				t.Fatalf("provider did not receive both linked results: %v", results)
			}
			if replay := post(srv, "phone", body("stale", false, "call-alpha", "call-beta"), t.Context()); replay.Code != http.StatusNotFound {
				t.Fatalf("stale handoff: %d %s", replay.Code, replay.Body)
			}
			if origin == "" {
				return
			}
			acceptedRun := waitNotificationNativeRun(t, srv, accepted.Header().Get("x-response-id"))
			<-acceptedRun.settled
			exact := body("exact", false, "call-beta", "call-alpha")
			for _, device := range []string{"other-phone", "phone"} {
				lifetime := t.Context()
				if device == "phone" {
					lifetime = revoked
				}
				if w := post(srv, device, exact, lifetime); w.Code != http.StatusNotFound {
					t.Fatalf("unauthorized replay (%s): %d %s", device, w.Code, w.Body)
				}
			}
			if w := post(srv, "phone", exact, t.Context()); w.Code != http.StatusOK || w.Header().Get("x-response-id") != acceptedRun.id {
				t.Fatalf("live claim replay: %d %s", w.Code, w.Body)
			}
			srv.responseRuns.delete(acceptedRun.id)
			restarted := newHost(false)
			oldInstance := srv.responseAdmissionInstance()
			if w := post(restarted, "phone", exact, t.Context(), oldInstance); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "client_message_already_committed") {
				t.Fatalf("receipt across old instance fence: %d %s", w.Code, w.Body)
			}
			for _, device := range []string{"phone", "other-phone"} {
				if w := post(restarted, device, body("new-key", false, "call-alpha", "call-beta"), t.Context(), oldInstance); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "server_instance_changed") {
					t.Fatalf("instance fence did not block new work: %d %s", w.Code, w.Body)
				}
			}
			for _, host := range []*serveServer{srv, restarted} {
				if w := post(host, "phone", exact, t.Context()); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "client_message_already_committed") {
					t.Fatalf("durable claim replay: %d %s", w.Code, w.Body)
				}
				if w := post(host, "other-phone", exact, t.Context()); w.Code != http.StatusNotFound {
					t.Fatalf("foreign durable claim replay: %d %s", w.Code, w.Body)
				}
				if w := post(host, "phone", strings.Replace(exact, "linked pong", "changed output", 1), t.Context()); w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "client_message_already_committed") {
					t.Fatalf("changed-body replay: %d %s", w.Code, w.Body)
				}
			}
			if provider.CurrentTurn() != 2 {
				t.Fatal("replay invoked the provider")
			}

		})
	}
}
