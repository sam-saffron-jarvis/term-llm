package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
)

func TestServeRuntimeCompactSessionPersistsAndReplacesActiveHistory(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sess := &session.Session{
		ID: "compact-session", Provider: "mock", ProviderKey: "mock", Model: "mock-model",
		Mode: session.ModeChat, Origin: session.OriginWeb, Status: session.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{
		llm.UserText("first question"), llm.AssistantText("first answer"),
		llm.UserText("second question"), llm.AssistantText("second answer"),
	}
	stored := make([]session.Message, 0, len(history))
	for i, message := range history {
		stored = append(stored, *session.NewMessage(sess.ID, message, i/2))
	}
	if err := store.ReplaceMessages(ctx, sess.ID, stored); err != nil {
		t.Fatal(err)
	}

	provider := llm.NewMockProvider("mock").AddTurn(llm.MockTurn{
		Text:  "Continue from the second answer.",
		Usage: llm.Usage{InputTokens: 20, OutputTokens: 5},
	})
	rt := &serveRuntime{
		provider: provider, providerKey: "mock", defaultModel: "mock-model",
		engine: llm.NewEngine(provider, nil), store: store, sessionMeta: sess,
		history: history, historyPersisted: true,
	}

	result, err := rt.compactSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("compactSession: %v", err)
	}
	if result == nil || result.OriginalCount != len(history) {
		t.Fatalf("result = %+v, want original count %d", result, len(history))
	}
	if len(rt.history) == 0 || !rt.historyPersisted {
		t.Fatalf("runtime history was not replaced: persisted=%v history=%+v", rt.historyPersisted, rt.history)
	}
	active, err := session.LoadActiveMessages(ctx, store, rt.sessionMeta)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != len(rt.history) {
		t.Fatalf("persisted active messages = %d, runtime history = %d", len(active), len(rt.history))
	}
	if rt.sessionMeta.CompactionCount != 1 {
		t.Fatalf("compaction count = %d, want 1", rt.sessionMeta.CompactionCount)
	}
	if rt.cumulativeUsage.InputTokens != 20 || rt.cumulativeUsage.OutputTokens != 5 {
		t.Fatalf("cumulative usage = %+v", rt.cumulativeUsage)
	}
}

func TestServeRuntimeCompactSessionRejectsBusyRuntime(t *testing.T) {
	rt := &serveRuntime{}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, err := rt.compactSession(context.Background(), "session"); !errors.Is(err, errServeSessionBusy) {
		t.Fatalf("compactSession error = %v, want errServeSessionBusy", err)
	}
}

func TestServeSessionRuntimeCompactEndpoint(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess := &session.Session{
		ID: "web-compact", Provider: "mock", ProviderKey: "mock", Model: "mock-model",
		Mode: session.ModeChat, Origin: session.OriginWeb, Status: session.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for _, message := range []llm.Message{llm.UserText("question"), llm.AssistantText("answer")} {
		if err := store.AddMessage(ctx, sess.ID, session.NewMessage(sess.ID, message, -1)); err != nil {
			t.Fatal(err)
		}
	}
	provider := llm.NewMockProvider("mock").AddTextResponse("Continue from the answer.")
	manager := newServeSessionManager(time.Minute, 4, func(context.Context) (*serveRuntime, error) {
		return &serveRuntime{
			provider: provider, providerKey: "mock", defaultModel: "mock-model",
			engine: llm.NewEngine(provider, nil), store: store,
		}, nil
	})
	t.Cleanup(manager.Close)
	server := &serveServer{sessionMgr: manager, store: store}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/web-compact/runtime/compact", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.handleSessionByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status/body = %d %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("response = %q", rr.Body.String())
	}
	stored, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CompactionCount != 1 {
		t.Fatalf("compaction count = %d, want 1", stored.CompactionCount)
	}
}
