package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestServeSideQuestionIsEphemeralAndToolless(t *testing.T) {
	provider := llm.NewMockProvider("mock").AddTextResponse("private answer")
	rt := &serveRuntime{
		providerKey: "mock", defaultModel: "test-model",
		history: []llm.Message{llm.UserText("main question"), llm.AssistantText("main answer")},
	}
	rt.refreshSideQuestionSnapshot(rt.history)
	rt.sideProviderFactory = func(_, _ string) (llm.Provider, error) { return provider, nil }

	events, err := rt.startSideQuestion(sideQuestionStart{Question: "clarify"})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	view := rt.sideQuestion.view()
	if view.Running || len(view.History) != 1 || view.History[0].Response != "private answer" {
		t.Fatalf("view = %#v", view)
	}
	if len(rt.history) != 2 {
		t.Fatalf("side content entered main history: %#v", rt.history)
	}
	if len(provider.Requests) != 1 {
		t.Fatalf("provider requests = %d", len(provider.Requests))
	}
	req := provider.Requests[0]
	if !req.Ephemeral || req.Search || len(req.Tools) != 0 || req.MaxTurns != 1 {
		t.Fatalf("unsafe side request: %#v", req)
	}
}

func TestServeSideQuestionEndpointsRecoverCancelAndClear(t *testing.T) {
	provider := llm.NewMockProvider("mock").AddTextResponse("answer")
	manager := newServeSessionManager(time.Minute, 4, func(context.Context) (*serveRuntime, error) {
		rt := &serveRuntime{providerKey: "mock", defaultModel: "m"}
		rt.sideProviderFactory = func(_, _ string) (llm.Provider, error) { return provider, nil }
		return rt, nil
	})
	defer manager.Close()
	s := &serveServer{sessionMgr: manager}

	body, _ := json.Marshal(sideQuestionStart{Question: "question"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/main/side-question", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleSideQuestion(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "done") {
		t.Fatalf("POST status/body = %d %q", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions/main/side-question", nil)
	rr = httptest.NewRecorder()
	s.handleSideQuestion(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "answer") {
		t.Fatalf("GET status/body = %d %q", rr.Code, rr.Body.String())
	}

	for _, suffix := range []string{"active", "history", "history"} {
		req = httptest.NewRequest(http.MethodDelete, "/api/sessions/main/side-question/"+suffix, nil)
		rr = httptest.NewRecorder()
		s.handleSideQuestion(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s status = %d", suffix, rr.Code)
		}
	}
}

func TestServeSideQuestionRejectsToolAttemptFromHistory(t *testing.T) {
	provider := llm.NewMockProvider("mock").AddToolCall("call", "shell", map[string]any{"command": "touch /tmp/no"})
	rt := &serveRuntime{providerKey: "mock", defaultModel: "m"}
	rt.sideProviderFactory = func(_, _ string) (llm.Provider, error) { return provider, nil }
	events, err := rt.startSideQuestion(sideQuestionStart{Question: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	view := rt.sideQuestion.view()
	if !view.Synthetic || len(view.History) != 0 {
		t.Fatalf("tool attempt state = %#v", view)
	}
}

type blockingSideProvider struct{}

func (blockingSideProvider) Name() string                   { return "blocking" }
func (blockingSideProvider) Credential() string             { return "test" }
func (blockingSideProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (blockingSideProvider) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	return &blockingSideStream{ctx: ctx}, nil
}

type blockingSideStream struct{ ctx context.Context }

func (s *blockingSideStream) Recv() (llm.Event, error) {
	<-s.ctx.Done()
	return llm.Event{}, s.ctx.Err()
}
func (*blockingSideStream) Close() error { return nil }

func TestServeSideQuestionConcurrencyAndCancellationAreIndependent(t *testing.T) {
	provider := blockingSideProvider{}
	rt := &serveRuntime{providerKey: "blocking", defaultModel: "m"}
	rt.sideProviderFactory = func(_, _ string) (llm.Provider, error) { return provider, nil }
	events, err := rt.startSideQuestion(sideQuestionStart{Question: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.startSideQuestion(sideQuestionStart{Question: "second"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second side start error = %v", err)
	}
	mainCtx, cancelMain := context.WithCancel(context.Background())
	cancelMain()
	if mainCtx.Err() == nil || !rt.sideQuestion.view().Running {
		t.Fatal("main cancellation affected side request")
	}
	rt.sideQuestion.cancelActive()
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("side cancellation did not stop provider")
	}
}
