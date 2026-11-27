package serve

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/session"
)

// A /thread path starts empty, so its notes are its only row and the browser
// has no previous_response_id for the first message. That first message must
// append after the notes rather than replace the transcript.
func TestHandleResponses_ThreadFirstMessageKeepsPathNote(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	source := &session.Session{ID: "thread-source", Provider: "mock", Model: "mock-model"}
	if err := store.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []*session.Message{
		session.NewMessage(source.ID, core.UserText("investigate"), -1),
		session.NewMessage(source.ID, core.AssistantText("found it"), -1),
	} {
		if err := store.AddMessage(ctx, source.ID, msg); err != nil {
			t.Fatal(err)
		}
	}
	thread, err := store.(session.ConversationBranchStore).CreateBranch(ctx, source.ID, session.CreateBranchOptions{IdempotencyKey: "thread"})
	if err != nil {
		t.Fatal(err)
	}
	threadID := thread.Session.ID
	note := session.NewPathNoteMessage(threadID, "- carried finding", core.PathNoteProvenance{SourceSessionID: source.ID}, -1)
	if err := store.AddMessage(ctx, threadID, note); err != nil {
		t.Fatal(err)
	}

	provider := mockprovider.NewMockProvider("mock").AddTextResponse("answer")
	runtime := &serveRuntime{
		provider:     provider,
		providerKey:  provider.Name(),
		engine:       engine.NewEngine(provider, nil),
		defaultModel: "mock-model",
		store:        store,
		systemPrompt: "agent instructions",
	}
	runtime.Touch()
	manager := newServeSessionManager(time.Minute, 10, func(context.Context) (*serveRuntime, error) { return runtime, nil })
	defer manager.Close()
	srv := &serveServer{store: store, sessionMgr: manager, cfgRef: &config.Config{DefaultProvider: "mock"}}

	if code, body := doResponsesFirstParty(t, srv, `{"input":"next step","client_message_id":"msg-next","model":"mock-model"}`, threadID); code != http.StatusOK {
		t.Fatalf("status = %d body=%#v", code, body)
	}

	messages, err := store.GetMessages(ctx, threadID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	noteIndex, userIndex := -1, -1
	for i, msg := range messages {
		roles = append(roles, string(msg.Role))
		if _, ok := msg.PathNoteProvenance(); ok {
			noteIndex = i
		}
		if msg.Role == core.RoleUser && msg.TextContent == "next step" {
			userIndex = i
		}
	}
	if noteIndex < 0 || userIndex < noteIndex {
		t.Fatalf("thread rows = %v, want the path note kept before the first message", roles)
	}
	requests := provider.RecordedRequests()
	if len(requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(requests))
	}
	var sent []string
	for _, msg := range requests[0].Messages {
		sent = append(sent, string(msg.Role))
	}
	if got := strings.Join(sent, ","); got != "system,developer,user" || !strings.Contains(requests[0].Messages[1].Parts[0].Text, "carried finding") {
		t.Fatalf("first thread turn sent %s, want the system prompt, then the path notes, then the message", got)
	}
}
