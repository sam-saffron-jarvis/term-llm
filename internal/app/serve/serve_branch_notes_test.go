package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/branchnotes"
	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/session"
)

type pathNotesGateProvider struct {
	*mockprovider.MockProvider
	stream func(context.Context) (core.Stream, error)
}

func (p pathNotesGateProvider) Stream(ctx context.Context, _ core.Request) (core.Stream, error) {
	return p.stream(ctx)
}

type pathNotesGateStream struct {
	ctx                context.Context
	previewed, release chan struct{}
	first              bool
	usage              *core.Usage
}

func (s *pathNotesGateStream) Recv() (core.Event, error) {
	if !s.first {
		s.first = true
		return core.Event{Type: core.EventTextDelta, Text: "- A useful finding"}, nil
	}
	if s.usage != nil {
		usage := s.usage
		s.usage = nil
		return core.Event{Type: core.EventUsage, Use: usage}, nil
	}
	close(s.previewed)
	select {
	case <-s.ctx.Done():
		return core.Event{}, s.ctx.Err()
	case <-s.release:
		return core.Event{}, io.EOF
	}
}
func (*pathNotesGateStream) Close() error { return nil }

func notesFixture(t *testing.T) (*serveServer, string, string, int64) {
	t.Helper()
	ctx := context.Background()
	store, err := session.NewStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "notes.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := &session.Session{ID: "notes-source", Name: "Forkable example", Provider: "mock", Model: "source-model"}
	if err := store.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	anchor := session.NewMessage(source.ID, core.UserText("before the branch"), -1)
	for _, m := range []*session.Message{anchor, session.NewMessage(source.ID, core.AssistantText("later finding"), -1)} {
		if err := store.AddMessage(ctx, source.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	child, err := store.(session.ConversationBranchStore).CreateBranch(ctx, source.ID, session.CreateBranchOptions{AnchorMessageID: anchor.ID, IdempotencyKey: "web"})
	if err != nil {
		t.Fatal(err)
	}
	srv := &serveServer{store: store}
	return srv, source.ID, child.Session.ID, anchor.ID
}

func notesRoute(s *serveServer, method, child, suffix string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/sessions/"+child+"/"+suffix, strings.NewReader(`{"mode":"notes"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSessionByID(w, r)
	return w
}

func waitNotesGate[T any](t *testing.T, gate <-chan T) T {
	t.Helper()
	select {
	case value := <-gate:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("path notes gate timed out")
		var zero T
		return zero
	}
}

func requireNotesJSON[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var data T
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatalf("status/body: %d %s, want %d", w.Code, w.Body, status)
	}
	return data
}

func TestWebPathNotesProgressCancelAndRejectReplay(t *testing.T) {
	srv, source, child, _ := notesFixture(t)
	reading, prepare, previewed, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
		close(reading)
		<-prepare
		return pathNotesGateProvider{mockprovider.NewMockProvider("mock"), func(ctx context.Context) (core.Stream, error) {
			return &pathNotesGateStream{ctx: ctx, previewed: previewed, release: release, usage: &core.Usage{InputTokens: 11}}, nil
		}}, nil
	}
	post := make(chan *httptest.ResponseRecorder, 1)
	go func() { post <- notesRoute(srv, http.MethodPost, child, "path-notes") }()
	waitNotesGate(t, reading)
	read := requireNotesJSON[wire.PathNotesStatus](t, notesRoute(srv, http.MethodGet, child, "path-notes"), 200)
	if read.State != "reading" || read.StartedAt == nil || read.SourceSessionID == "" {
		t.Fatalf("reading: %+v", read)
	}
	close(prepare)
	waitNotesGate(t, previewed)
	status := requireNotesJSON[wire.PathNotesStatus](t, notesRoute(srv, http.MethodGet, child, "path-notes"), 200)
	if status.State != "writing" || status.WritingAt == nil || status.SourceMessages == nil || *status.SourceMessages != 1 || status.Model != "source-model" || status.Preview != "- A useful finding" {
		t.Fatalf("writing: %+v", status)
	}
	tree := requireNotesJSON[webBranchTreeResponse](t, notesRoute(srv, http.MethodGet, child, "tree"), 200)
	if tree.PathNotes == nil || tree.PathNotes.Preview != status.Preview {
		t.Fatalf("tree snapshot: %+v", tree.PathNotes)
	}
	cancel := requireNotesJSON[wire.PathNotesCancellation](t, notesRoute(srv, http.MethodDelete, child, "path-notes"), 200)
	if !cancel.Cancelled || cancel.NoteSaved {
		t.Fatalf("cancel: %+v", cancel)
	}
	requireCancelledPOST(t, waitNotesGate(t, post))
	usage, err := srv.store.ListModelUsage(context.Background(), source)
	if err != nil || len(usage) != 1 || usage[0].InputTokens != 11 || usage[0].Kind != session.ModelUsageHandover {
		t.Fatalf("cancelled helper usage: %+v %v", usage, err)
	}
	if notes := requireNotesJSON[wire.PathNotesStatus](t, notesRoute(srv, http.MethodGet, child, "path-notes"), 200); notes.State != "idle" {
		t.Fatalf("idle: %+v", notes)
	}
	if tree := requireNotesJSON[webBranchTreeResponse](t, notesRoute(srv, http.MethodGet, child, "tree"), 200); tree.PathNotes != nil {
		t.Fatal("idle tree included notes snapshot")
	}
	messages, _ := srv.store.GetMessages(context.Background(), child, 0, 0)
	if len(messages) != 1 {
		t.Fatalf("cancelled note was saved: %v", messages)
	}
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
		t.Fatal("replayed POST started a helper")
		return nil, nil
	}
	requireCancelledPOST(t, notesRoute(srv, http.MethodPost, child, "path-notes"))
}

func TestWebPathNotesRejectLatePlacement(t *testing.T) {
	for _, during := range []bool{false, true} {
		name := "before POST"
		if during {
			name = "during generation"
		}
		t.Run(name, func(t *testing.T) {
			srv, _, child, _ := notesFixture(t)
			addTurn := func() {
				if err := srv.store.AddMessage(context.Background(), child, session.NewMessage(child, core.UserText("new work"), -1)); err != nil {
					t.Error(err)
				}
			}
			previewed, release := make(chan struct{}), make(chan struct{})
			srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
				return pathNotesGateProvider{mockprovider.NewMockProvider("mock"), func(ctx context.Context) (core.Stream, error) {
					return &pathNotesGateStream{ctx: ctx, previewed: previewed, release: release}, nil
				}}, nil
			}
			if !during {
				addTurn()
				close(release)
			}
			post := make(chan *httptest.ResponseRecorder, 1)
			go func() { post <- notesRoute(srv, http.MethodPost, child, "path-notes") }()
			if during {
				waitNotesGate(t, previewed)
				addTurn()
				close(release)
			}
			if w := waitNotesGate(t, post); w.Code != http.StatusConflict {
				t.Fatalf("late POST: %d %s", w.Code, w.Body)
			}
			messages, _ := srv.store.GetMessages(context.Background(), child, 0, 0)
			if len(messages) != 2 {
				t.Fatalf("late note was appended: %v", messages)
			}
		})
	}
}

func TestWebPathNotesRouteAuth(t *testing.T) {
	srv, _, child, _ := notesFixture(t)
	srv.cfg.requireAuth, srv.cfg.token = true, "mock-token"
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPost} {
		for _, valid := range []bool{false, true} {
			r := httptest.NewRequest(method, "/v1/sessions/"+child+"/path-notes", strings.NewReader(`{"mode":"notes"}`))
			r.Header.Set("Content-Type", "application/json")
			want := http.StatusUnauthorized
			if valid {
				want = http.StatusOK
				r.Header.Set("Authorization", "Bearer mock-token")
			}
			w := httptest.NewRecorder()
			srv.auth(srv.handleSessionByID)(w, r)
			if w.Code != want {
				t.Fatalf("%s auth=%t: %d %s", method, valid, w.Code, w.Body)
			}
		}
	}
}

func TestWebPathNotesCancelDoesNotAffectIndependentDurableWork(t *testing.T) {
	srv, source, child, anchor := notesFixture(t)
	webPreview, webRelease, flightPreview, flightRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	providers := make(chan core.Provider, 2)
	for _, pair := range [][2]chan struct{}{{webPreview, webRelease}, {flightPreview, flightRelease}} {
		providers <- pathNotesGateProvider{mockprovider.NewMockProvider("mock"), func(ctx context.Context) (core.Stream, error) {
			return &pathNotesGateStream{ctx: ctx, previewed: pair[0], release: pair[1]}, nil
		}}
	}
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) { return <-providers, nil }
	web := make(chan *httptest.ResponseRecorder, 1)
	go func() { web <- notesRoute(srv, http.MethodPost, child, "path-notes") }()
	waitNotesGate(t, webPreview)
	mode := &responsesBranchContextRequest{Mode: "notes"}
	result := make(chan *session.BranchPathNote, 1)
	go func() {
		note, _, _, _, done := srv.prepareBranchPathNoteOnce(context.Background(), source, anchor, "durable", mode)
		defer done()
		result <- note
	}()
	waitNotesGate(t, flightPreview)
	if w := notesRoute(srv, http.MethodDelete, child, "path-notes"); w.Code != 200 {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	requireCancelledPOST(t, waitNotesGate(t, web))
	close(flightRelease)
	if note := waitNotesGate(t, result); note == nil || note.Text != "- A useful finding" {
		t.Fatalf("independent durable note: %+v", note)
	}
}

func requireCancelledPOST(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	result := requireNotesJSON[wire.PreparePathNotesResponse](t, w, http.StatusOK)
	if !result.Ready || result.NoteSaved || !result.Cancelled {
		t.Fatalf("skipped POST: %+v", result)
	}
}

func TestWebPathNotesSkipBeforePOSTAndMissingStatus(t *testing.T) {
	srv, source, child, _ := notesFixture(t)
	requireNotesJSON[wire.PathNotesStatus](t, notesRoute(srv, http.MethodGet, source, "path-notes"), 200)
	if w := notesRoute(srv, http.MethodGet, "missing", "path-notes"); w.Code != 404 {
		t.Fatalf("missing GET: %d %s", w.Code, w.Body)
	}
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
		t.Fatal("a delayed POST ran the helper")
		return nil, nil
	}
	cancel := requireNotesJSON[wire.PathNotesCancellation](t, notesRoute(srv, http.MethodDelete, child, "path-notes"), 200)
	if !cancel.Cancelled || cancel.NoteSaved {
		t.Fatalf("skip: %+v", cancel)
	}
	requireCancelledPOST(t, notesRoute(srv, http.MethodPost, child, "path-notes"))
}

func TestWebPathNotesDeleteDuringSaveReturnsSnapshot(t *testing.T) {
	srv, source, child, anchor := notesFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := branchnotes.NewJob(wire.PathNotesStatus{State: "writing"}, cancel)
	srv.branchNotes.Start(child, job)
	if !job.BeginSave(ctx) {
		t.Fatal("could not enter saving")
	}
	response := requireNotesJSON[wire.PathNotesCancellation](t, notesRoute(srv, http.MethodDelete, child, "path-notes"), 200)
	if response.Cancelled || response.NoteSaved || response.Status == nil || response.Status.State != "saving" || ctx.Err() != nil {
		t.Fatalf("saving DELETE: %+v, work error: %v", response, ctx.Err())
	}
	note := session.NewPathNoteMessage(child, "finished", core.PathNoteProvenance{SourceSessionID: source, AnchorMessageID: anchor}, -1)
	if err := srv.store.AddMessage(ctx, child, note); err != nil {
		t.Fatal(err)
	}
	srv.branchNotes.Finish(child, job)
	response = requireNotesJSON[wire.PathNotesCancellation](t, notesRoute(srv, http.MethodDelete, child, "path-notes"), 200)
	if !response.NoteSaved || response.Cancelled {
		t.Fatalf("saved DELETE: %+v", response)
	}
}

func TestWebPathNotesDeadlineIsNotRequestTimeout(t *testing.T) {
	srv, _, child, _ := notesFixture(t)
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
		return mockprovider.NewMockProvider("mock").AddError(context.DeadlineExceeded), nil
	}
	if w := notesRoute(srv, http.MethodPost, child, "path-notes"); w.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline POST: %d %s", w.Code, w.Body)
	}
}

func TestWebPathNotesBusyChildCanRetry(t *testing.T) {
	srv, _, child, _ := notesFixture(t)
	runtime := &serveRuntime{}
	srv.sessionMgr = &serveSessionManager{sessions: map[string]*serveRuntime{child: runtime}}
	runtime.mu.Lock()
	w := notesRoute(srv, http.MethodPost, child, "path-notes")
	runtime.mu.Unlock()
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "busy; try again") {
		t.Fatalf("busy POST: %d %s", w.Code, w.Body)
	}
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) {
		return mockprovider.NewMockProvider("mock").AddTextResponse("Retry worked"), nil
	}
	if result := requireNotesJSON[wire.PreparePathNotesResponse](t, notesRoute(srv, http.MethodPost, child, "path-notes"), 200); !result.NoteSaved {
		t.Fatalf("retry: %+v", result)
	}
}

// A /thread starts an empty path at anchor zero, so its notes summarize the
// whole source conversation.
func TestWebPathNotesForThreadReadWholeConversation(t *testing.T) {
	srv, source, _, _ := notesFixture(t)
	ctx := context.Background()
	thread, err := srv.store.(session.ConversationBranchStore).CreateBranch(ctx, source, session.CreateBranchOptions{IdempotencyKey: "thread"})
	if err != nil {
		t.Fatal(err)
	}
	provider := mockprovider.NewMockProvider("mock").AddTextResponse("- Whole conversation")
	srv.pathNotesProviderFactory = func(_, _ string) (core.Provider, error) { return provider, nil }
	if result := requireNotesJSON[wire.PreparePathNotesResponse](t, notesRoute(srv, http.MethodPost, thread.Session.ID, "path-notes"), 200); !result.NoteSaved {
		t.Fatalf("thread notes: %+v", result)
	}
	messages, err := srv.store.GetMessages(ctx, thread.Session.ID, 0, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("thread messages: %v %v", messages, err)
	}
	provenance, ok := messages[0].PathNoteProvenance()
	if !ok || provenance.SourceSessionID != source || provenance.AnchorMessageID != 0 || provenance.SourceMessages != 2 {
		t.Fatalf("thread note provenance: %+v %v", provenance, ok)
	}
}
