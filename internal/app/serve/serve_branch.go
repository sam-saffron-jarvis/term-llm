package serve

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/app/serve/branchnotes"
	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/session"
)

type webBranchTreePoint = branchnotes.BranchTreePoint

type webBranchTreeResponse struct {
	session.BranchTree
	BranchPoints []webBranchTreePoint  `json:"branch_points"`
	PathNotes    *wire.PathNotesStatus `json:"path_notes,omitempty"`
}

type createSessionBranchRequest struct {
	AnchorMessageID int64 `json:"anchor_message_id"`
	// LegacyExpectedRev is accepted and ignored for backward wire compatibility.
	// Deprecated: this Web endpoint identifies an immutable transcript prefix by
	// anchor_message_id; lower-level CreateBranch and /v1/responses retain their
	// distinct full-head compare-and-swap contracts.
	LegacyExpectedRev *int64 `json:"expected_rev,omitempty"`
	IdempotencyKey    string `json:"idempotency_key"`
}

type createSessionBranchResponse struct {
	Session               webSessionEntry `json:"session"`
	ParentSessionID       string          `json:"parent_session_id"`
	ParentTitle           string          `json:"parent_title"`
	ForkAfterMessageID    int64           `json:"fork_after_message_id,omitempty"`
	CopiedAnchorMessageID int64           `json:"copied_anchor_message_id,omitempty"`
	Reused                bool            `json:"reused,omitempty"`
}

// activeWebBranchRun is one consistent sample of the source session's active
// run, taken under the run lock.
type activeWebBranchRun struct {
	responseID string
	epoch      int64
	// anchorRowID is the latest published durable boundary; zero while it is
	// withdrawn after a mid-run rewrite or compaction.
	anchorRowID int64
	// startAnchorRowID is the durable boundary published before run output.
	startAnchorRowID int64
}

func (s *serveServer) sampleActiveWebBranchRun(sessionID string) activeWebBranchRun {
	if s.responseRuns == nil {
		return activeWebBranchRun{}
	}
	id := s.responseRuns.activeRunID(sessionID)
	if id == "" {
		return activeWebBranchRun{}
	}
	run, ok := s.responseRuns.get(id)
	if !ok || run == nil {
		return activeWebBranchRun{responseID: id}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	sample := activeWebBranchRun{responseID: id, epoch: run.runEpoch, startAnchorRowID: run.startAnchorRowID}
	if run.anchorAvailable {
		sample.anchorRowID = run.anchorRowID
	}
	return sample
}

func branchableRowPresent(messages []session.Message, rowID int64) bool {
	if rowID <= 0 {
		return false
	}
	for _, message := range messages {
		if message.ID == rowID {
			return session.IsBranchableMessage(message)
		}
	}
	return false
}

// activeWebBranchAnchorRowID returns the last row of messages that the active
// run can no longer change: 0 when no run is active, -1 when nothing is safe.
func activeWebBranchAnchorRowID(messages []session.Message, run activeWebBranchRun) int64 {
	if strings.TrimSpace(run.responseID) == "" {
		return 0
	}
	if branchableRowPresent(messages, run.anchorRowID) {
		return run.anchorRowID
	}
	// While the published boundary is withdrawn, the boundary that preceded run
	// output still bounds history the run cannot change. Rewrites that alter it
	// delete the row, which fails closed below; message IDs are AUTOINCREMENT,
	// so a deleted ID never returns with different content.
	if branchableRowPresent(messages, run.startAnchorRowID) {
		return run.startAnchorRowID
	}
	return -1
}

func pruneActiveWebBranchOutput(messages []session.Message, activeResponseID string, activeAnchorRowID int64) []session.Message {
	return branchnotes.PruneActiveBranchOutput(messages, activeResponseID, activeAnchorRowID)
}

type activeWebBranchAnchorStatus uint8

const (
	activeWebBranchAnchorSafe activeWebBranchAnchorStatus = iota
	activeWebBranchAnchorMissing
	activeWebBranchAnchorInvalid
	activeWebBranchAnchorUnstable
)

func activeWebBranchAnchorSafety(messages []session.Message, run activeWebBranchRun, requestedAnchorRowID int64) activeWebBranchAnchorStatus {
	if requestedAnchorRowID == 0 {
		return activeWebBranchAnchorSafe
	}
	var requested *session.Message
	for i := range messages {
		if messages[i].ID == requestedAnchorRowID {
			requested = &messages[i]
			break
		}
	}
	if requested == nil {
		return activeWebBranchAnchorMissing
	}
	if !session.IsBranchableMessage(*requested) {
		return activeWebBranchAnchorInvalid
	}
	activeAnchorRowID := activeWebBranchAnchorRowID(messages, run)
	if activeAnchorRowID <= 0 {
		return activeWebBranchAnchorUnstable
	}
	var activeAnchor *session.Message
	for i := range messages {
		if messages[i].ID == activeAnchorRowID {
			activeAnchor = &messages[i]
			break
		}
	}
	if activeAnchor == nil || requested.Sequence > activeAnchor.Sequence {
		return activeWebBranchAnchorUnstable
	}
	return activeWebBranchAnchorSafe
}

const webBranchSnapshotMaxAttempts = 3

var errWebBranchSnapshotUnstable = errors.New("conversation branch snapshot changed while loading")

type webBranchContextSnapshot struct {
	messages          []session.Message
	activeAnchorRowID int64
	limited           bool
}

func (s *serveServer) loadWebBranchContextSnapshot(ctx context.Context, sessionID string) (webBranchContextSnapshot, error) {
	for attempt := 0; attempt < webBranchSnapshotMaxAttempts; attempt++ {
		run := s.sampleActiveWebBranchRun(sessionID)
		messages, err := s.store.GetMessages(ctx, sessionID, 0, 0)
		if err != nil {
			return webBranchContextSnapshot{}, err
		}
		if run != s.sampleActiveWebBranchRun(sessionID) {
			if err := ctx.Err(); err != nil {
				return webBranchContextSnapshot{}, err
			}
			continue
		}
		if run.responseID == "" {
			return webBranchContextSnapshot{messages: messages}, nil
		}
		activeAnchorRowID := activeWebBranchAnchorRowID(messages, run)
		safeMessages := pruneActiveWebBranchOutput(messages, run.responseID, activeAnchorRowID)
		return webBranchContextSnapshot{
			messages:          safeMessages,
			activeAnchorRowID: activeAnchorRowID,
			limited:           len(safeMessages) < len(messages),
		}, nil
	}
	return webBranchContextSnapshot{}, errWebBranchSnapshotUnstable
}

func webBranchTreePointsForActiveRun(messages []session.Message, activeAnchorRowID int64) []webBranchTreePoint {
	return branchnotes.BranchTreePoints(messages, activeAnchorRowID)
}

func webBranchTreePoints(messages []session.Message) []webBranchTreePoint {
	return webBranchTreePointsForActiveRun(messages, 0)
}

func (s *serveServer) handleSessionTree(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s == nil || s.store == nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session history is unavailable")
		return
	}
	store, ok := s.store.(session.ConversationBranchStore)
	if !ok {
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branching is unavailable")
		return
	}
	tree, err := store.GetBranchTree(r.Context(), sessionID)
	switch {
	case errors.Is(err, session.ErrNotFound):
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session not found")
	case errors.Is(err, session.ErrBranchingUnsupported):
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branching is unavailable")
	case err != nil:
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to load conversation tree")
	default:
		response := webBranchTreeResponse{BranchTree: tree, PathNotes: s.activePathNotesStatus(sessionID)}
		if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_branch_points")), "1") ||
			strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_branch_points")), "true") {
			snapshot, snapshotErr := s.loadWebBranchContextSnapshot(r.Context(), sessionID)
			switch {
			case errors.Is(snapshotErr, errWebBranchSnapshotUnstable):
				writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branch points are changing; refresh and try again")
				return
			case snapshotErr != nil:
				writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to load conversation branch points")
				return
			}
			response.BranchPoints = webBranchTreePointsForActiveRun(snapshot.messages, snapshot.activeAnchorRowID)
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func (s *serveServer) handleCreateSessionBranch(w http.ResponseWriter, r *http.Request, sourceSessionID string) {
	if s == nil || s.store == nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session history is unavailable")
		return
	}
	branchStore, ok := s.store.(session.ConversationBranchStore)
	if !ok {
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branching is unavailable")
		return
	}
	var req createSessionBranchRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "idempotency_key is required")
		return
	}
	source, err := s.store.Get(r.Context(), sourceSessionID)
	if err != nil || source == nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "session not found")
		return
	}
	if replayStore, ok := s.store.(session.ConversationBranchReplayStore); ok {
		replay, found, replayErr := replayStore.GetBranchByIdempotencyKey(r.Context(), sourceSessionID, req.IdempotencyKey)
		switch {
		case errors.Is(replayErr, session.ErrBranchingUnsupported):
			writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branching is unavailable")
			return
		case replayErr != nil:
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to resolve conversation branch")
			return
		case found && replay.Session != nil:
			if replay.ForkAfterMessageID != req.AnchorMessageID {
				writeOpenAIError(w, http.StatusConflict, "conflict_error", "idempotency key was already used for a different branch point")
				return
			}
			writeJSON(w, http.StatusOK, createSessionBranchResponse{
				Session:               s.webSessionEntryFromBranch(replay.Session, source),
				ParentSessionID:       sourceSessionID,
				ParentTitle:           source.PreferredShortTitle(),
				ForkAfterMessageID:    replay.ForkAfterMessageID,
				CopiedAnchorMessageID: replay.AnchorMessageID,
				Reused:                true,
			})
			return
		}
	}
	activeRun := s.sampleActiveWebBranchRun(sourceSessionID)
	activeSource := activeRun.responseID != ""
	unlock := func() {}
	validateActiveAnchor := func() bool {
		messages, ok := s.loadActiveWebBranchMessages(w, r, sourceSessionID, &activeRun)
		if !ok {
			return false
		}
		status := activeWebBranchAnchorSafety(messages, activeRun, req.AnchorMessageID)
		switch status {
		case activeWebBranchAnchorMissing:
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "branch source or anchor was not found")
			return false
		case activeWebBranchAnchorInvalid:
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "branch anchor is not a durable continuation boundary")
			return false
		case activeWebBranchAnchorUnstable:
			writeOpenAIError(w, http.StatusConflict, "conflict_error", "branch point is not stable while source work is active")
			return false
		default:
			return true
		}
	}
	if activeSource {
		if !validateActiveAnchor() {
			return
		}
	} else {
		var busy bool
		unlock, busy = s.lockBranchSourceRuntime(sourceSessionID)
		if busy {
			// Work may have become active after the initial sample. Prefer its
			// published durable boundary over rejecting an otherwise safe prefix.
			activeRun = s.sampleActiveWebBranchRun(sourceSessionID)
			activeSource = activeRun.responseID != ""
			if !activeSource {
				writeOpenAIError(w, http.StatusConflict, "conflict_error", "cannot branch while source work is active")
				return
			}
			unlock = func() {}
			if !validateActiveAnchor() {
				return
			}
		}
	}
	defer unlock()
	// This Web API is anchor/prefix-based. LegacyExpectedRev is decoded only for
	// backward wire compatibility and intentionally has no precondition effect:
	// unrelated suffix drift cannot mutate the immutable row-ID prefix. The
	// transaction still resolves and validates that anchor; lower-level callers
	// retain ExpectedRev/ExpectedState compare-and-swap behavior.
	result, err := branchStore.CreateBranch(r.Context(), sourceSessionID, session.CreateBranchOptions{
		CreatedBy:       identityID(r.Context()),
		AnchorMessageID: req.AnchorMessageID,
		IdempotencyKey:  req.IdempotencyKey,
	})
	switch {
	case errors.Is(err, session.ErrBranchConflict):
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation changed in another client; refresh and try again")
	case errors.Is(err, session.ErrBranchIdempotencyConflict):
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "idempotency key was already used for a different branch point")
	case errors.Is(err, session.ErrBranchingUnsupported):
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "conversation branching is unavailable")
	case errors.Is(err, session.ErrNotFound) && activeSource:
		writeOpenAIError(w, http.StatusConflict, "conflict_error", "the active branch boundary changed; refresh and try again")
	case errors.Is(err, session.ErrNotFound):
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "branch source or anchor was not found")
	case err != nil:
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to create conversation branch")
	case result.Session == nil:
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to create conversation branch")
	default:
		if !result.Reused {
			s.publishEvent(serveEventInput{Type: serveEventSessionCreated, SessionID: result.Session.ID, ParentSessionID: sourceSessionID, Reason: "branch"})
			s.publishEvent(serveEventInput{Type: serveEventChildrenChanged, SessionID: result.Session.ID, ParentSessionID: sourceSessionID, Reason: "branch_created"})
		}
		writeJSON(w, http.StatusCreated, createSessionBranchResponse{
			Session:               s.webSessionEntryFromBranch(result.Session, source),
			ParentSessionID:       sourceSessionID,
			ParentTitle:           source.PreferredShortTitle(),
			ForkAfterMessageID:    result.ForkAfterMessageID,
			CopiedAnchorMessageID: result.AnchorMessageID,
			Reused:                result.Reused,
		})
	}
}

// loadActiveWebBranchMessages reads the source transcript consistently with
// *run. Boundary movement within the same run is resampled a bounded number of
// times; a different run means the sample no longer describes these rows.
func (s *serveServer) loadActiveWebBranchMessages(w http.ResponseWriter, r *http.Request, sessionID string, run *activeWebBranchRun) ([]session.Message, bool) {
	for attempt := 0; attempt < webBranchSnapshotMaxAttempts; attempt++ {
		messages, err := s.store.GetMessages(r.Context(), sessionID, 0, 0)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to validate active conversation branch")
			return nil, false
		}
		current := s.sampleActiveWebBranchRun(sessionID)
		if current == *run {
			return messages, true
		}
		if current.responseID != run.responseID || current.epoch != run.epoch {
			break
		}
		*run = current
	}
	writeOpenAIError(w, http.StatusConflict, "conflict_error", "the active branch boundary changed; refresh and try again")
	return nil, false
}

func (s *serveServer) webSessionEntryFromBranch(child, source *session.Session) webSessionEntry {
	entry := s.webSessionEntryFromSession(child)
	entry.BranchParentSessionID, entry.BranchParentNumber = source.ID, source.Number
	return entry
}
