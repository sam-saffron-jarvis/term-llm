package serve

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/branchnotes"
	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/session/transcript"
)

func (s *serveServer) activePathNotesStatus(childID string) *wire.PathNotesStatus {
	job, ok := s.branchNotes.Load(childID)
	if !ok {
		return nil
	}
	return job.Snapshot()
}

func (s *serveServer) routeSessionPathNotes(w http.ResponseWriter, r *http.Request, childID string) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		status := s.activePathNotesStatus(childID)
		if status == nil {
			if code := branchnotes.IdleStatusCode(r.Context(), s.store, childID); code != http.StatusOK {
				(branchNoteError{code, http.StatusText(code)}).write(w)
				return
			}
			status = &wire.PathNotesStatus{State: "idle"}
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodDelete:
		s.cancelSessionPathNotes(w, r, childID)
	case http.MethodPost:
		if err := requireJSONContentType(r); err != nil {
			writeOpenAIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", err.Error())
			return
		}
		s.handleSessionPathNotes(w, r, childID)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

type branchNoteError struct {
	status  int
	message string
}

func (failure branchNoteError) write(w http.ResponseWriter) {
	kind := "invalid_request_error"
	switch {
	case failure.status == http.StatusConflict:
		kind = "conflict_error"
	case failure.status == http.StatusNotFound:
		kind = "not_found_error"
	case failure.status >= http.StatusInternalServerError:
		kind = "server_error"
	}
	writeOpenAIError(w, failure.status, kind, failure.message)
}

func (s *serveServer) pathNoteEdge(ctx context.Context, childID string) (session.BranchTreeNode, branchNoteError) {
	if s.store == nil {
		return session.BranchTreeNode{}, branchNoteError{http.StatusNotFound, "session history is unavailable"}
	}
	store, ok := s.store.(session.ConversationBranchStore)
	if !ok {
		return session.BranchTreeNode{}, branchNoteError{http.StatusConflict, "conversation branching is unavailable"}
	}
	tree, err := store.GetBranchTree(ctx, childID)
	if err != nil {
		return session.BranchTreeNode{}, branchNoteError{http.StatusNotFound, "conversation branch was not found"}
	}
	edge, ok := branchnotes.DirectBranchNode(tree, childID)
	if !ok {
		return edge, branchNoteError{http.StatusConflict, "session is not a conversation branch"}
	}
	return edge, branchNoteError{}
}

func (s *serveServer) cancelSessionPathNotes(w http.ResponseWriter, r *http.Request, childID string) {
	edge, failure := s.pathNoteEdge(r.Context(), childID)
	if failure.status != 0 {
		failure.write(w)
		return
	}
	result, err := s.branchNotes.Cancel(r.Context(), s.store, edge)
	if err != nil {
		(branchNoteError{http.StatusInternalServerError, "failed to inspect branch context"}).write(w)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *serveServer) handleSessionPathNotes(w http.ResponseWriter, r *http.Request, childID string) {
	var req wire.PreparePathNotesRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	mode, focus, status, message := branchContextRequestValues(&responsesBranchContextRequest{Mode: req.Mode, Focus: req.Focus})
	if status == 0 && mode != "notes" && mode != "focused" {
		status, message = http.StatusBadRequest, "mode must be notes or focused"
	}
	if status != 0 {
		(branchNoteError{status, message}).write(w)
		return
	}
	edge, failure := s.pathNoteEdge(r.Context(), childID)
	if failure.status != 0 {
		failure.write(w)
		return
	}
	workCtx, cancel := detachedBranchWorkContext(r.Context())
	defer cancel()
	now := time.Now()
	job := branchnotes.NewJob(wire.PathNotesStatus{State: "reading", Mode: mode, Focus: focus, SourceSessionID: edge.ParentSessionID, StartedAt: &now}, cancel)
	active, skipped := s.branchNotes.Start(childID, job)
	if skipped {
		writeJSON(w, http.StatusOK, wire.PreparePathNotesResponse{Ready: true, Cancelled: true})
		return
	}
	if active {
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "branch context generation is already active")
		return
	}
	result, failure := func() (wire.PreparePathNotesResponse, branchNoteError) {
		defer s.branchNotes.Finish(childID, job)
		return s.prepareWebPathNote(workCtx, edge, job, mode, focus)
	}()
	if job.Skipped() {
		writeJSON(w, http.StatusOK, wire.PreparePathNotesResponse{Ready: true, Cancelled: true})
		return
	}
	if failure.status != 0 {
		failure.write(w)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func pathNoteWorkError(ctx context.Context) branchNoteError {
	code, message := branchnotes.WorkError(ctx)
	return branchNoteError{code, message}
}

var errPathNoteLate = branchNoteError{http.StatusConflict, "the conversation path already continued without notes"}

func (s *serveServer) prepareWebPathNote(ctx context.Context, edge session.BranchTreeNode, job *branchnotes.Job, mode, focus string) (wire.PreparePathNotesResponse, branchNoteError) {
	result := wire.PreparePathNotesResponse{Ready: true}
	messages, err := s.store.GetMessages(ctx, edge.SessionID, 0, 0)
	if ctx.Err() != nil {
		return result, pathNoteWorkError(ctx)
	}
	if err != nil {
		return result, branchNoteError{http.StatusInternalServerError, "failed to inspect branch context"}
	}
	if branchnotes.ExistingPathNote(messages, edge.ParentSessionID, edge.ForkAfterMessageID) {
		result.Reused, result.NoteSaved = true, true
		return result, branchNoteError{}
	}
	if len(messages) > 0 && messages[len(messages)-1].Sequence > edge.ForkAfterSequence {
		return result, errPathNoteLate
	}
	unlock, busy := s.lockBranchSourceRuntime(edge.SessionID)
	if busy {
		return result, branchNoteError{http.StatusConflict, "the conversation path is busy; try again"}
	}
	unlock()
	return s.generateWebPathNote(ctx, edge, job, mode, focus, result)
}

func (s *serveServer) generateWebPathNote(ctx context.Context, edge session.BranchTreeNode, job *branchnotes.Job, mode, focus string, result wire.PreparePathNotesResponse) (wire.PreparePathNotesResponse, branchNoteError) {
	snapshot, err := s.loadWebBranchContextSnapshot(ctx, edge.ParentSessionID)
	switch {
	case ctx.Err() != nil:
		return result, pathNoteWorkError(ctx)
	case errors.Is(err, errWebBranchSnapshotUnstable):
		return result, branchNoteError{http.StatusConflict, "branch context is changing; try again"}
	case err != nil:
		return result, branchNoteError{http.StatusInternalServerError, "failed to load branch context"}
	}
	source, sourceErr := session.MessagesAfterBranchAnchor(snapshot.messages, edge.ForkAfterMessageID)
	if sourceErr != nil && !snapshot.limited {
		return result, branchNoteError{http.StatusBadRequest, "branch source or anchor was not found"}
	}
	result.Limited = snapshot.limited
	if snapshot.limited {
		result.Message = "Context was prepared from the latest durable completed part of the active source; in-progress output was omitted."
	}
	if sourceErr != nil || len(source) == 0 {
		if snapshot.limited {
			result.Message = "The path was created without additional notes because no later source context was available."
		}
		return result, branchNoteError{}
	}
	// Unlike /v1/responses, a web job owns generation exclusively for its child.
	pathNote, status, message := s.generateBranchPathNote(ctx, edge.ParentSessionID, source, mode, engine.PathNotesConfig{
		Focus: focus, OnPrepared: job.Prepared, OnText: job.Text, OnTextReset: job.ResetText,
	})
	if ctx.Err() != nil {
		return result, pathNoteWorkError(ctx)
	}
	if status != 0 {
		return result, branchNoteError{status, message}
	}
	if pathNote == nil {
		return result, branchNoteError{}
	}
	failure := s.saveWebPathNote(ctx, edge, job, pathNote)
	result.NoteSaved = failure.status == 0
	return result, failure
}

func (s *serveServer) saveWebPathNote(ctx context.Context, edge session.BranchTreeNode, job *branchnotes.Job, pathNote *session.BranchPathNote) branchNoteError {
	if !job.BeginSave(ctx) {
		return pathNoteWorkError(ctx)
	}
	pathNote.Provenance.SourceSessionID = edge.ParentSessionID
	pathNote.Provenance.AnchorMessageID = edge.ForkAfterMessageID
	note := session.NewPathNoteMessage(edge.SessionID, pathNote.Text, pathNote.Provenance, -1)
	rev, err := transcript.AddMessageWithTranscriptRev(session.WithPathNotePrefix(ctx, edge.ForkAfterSequence), s.store, edge.SessionID, note)
	if ctx.Err() != nil && err != nil {
		return pathNoteWorkError(ctx)
	}
	if errors.Is(err, session.ErrBranchConflict) {
		return errPathNoteLate
	}
	if err != nil {
		return branchNoteError{http.StatusInternalServerError, "failed to save branch context"}
	}
	s.publishEvent(serveEventInput{Type: serveEventSessionTranscriptChanged, SessionID: edge.SessionID, ParentSessionID: edge.ParentSessionID, TranscriptRev: rev, Reason: "branch_path_note"})
	return branchNoteError{}
}
