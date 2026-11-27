package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session"
)

// A fork's path note is stored as a developer row; the web transcript must
// still deliver it so the browser can render the notes card.
func TestHandleSessionTranscriptBodiesDeliversPathNote(t *testing.T) {
	srv, store, sess := newTranscriptHandlerServer(t)
	ctx := context.Background()
	user := session.NewMessage(sess.ID, core.UserText("question"), -1)
	answer := session.NewMessage(sess.ID, core.AssistantText("answer"), -1)
	note := session.NewPathNoteMessage(sess.ID, "- carried finding", core.PathNoteProvenance{SourceSessionID: "source", AnchorMessageID: 9}, -1)
	for _, msg := range []*session.Message{user, answer, note} {
		if err := store.AddMessage(ctx, sess.ID, msg); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sess.ID+"/transcript/bodies?ids="+strconv.FormatInt(user.ID, 10), nil)
	rr := httptest.NewRecorder()
	srv.handleSessionByID(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Messages []sessionMessageEntry `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 3 || body.Messages[2].Role != "path-note" {
		t.Fatalf("path note missing from transcript bodies: %s", rr.Body.String())
	}
	parts := body.Messages[2].Parts
	if len(parts) != 1 || parts[0].PathNote == nil || parts[0].PathNote.SourceSessionID != "source" || parts[0].Text != "- carried finding" {
		t.Fatalf("path note part = %+v", parts)
	}
}
