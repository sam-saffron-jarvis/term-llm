package cmd

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/samsaffron/term-llm/internal/filetrack"
)

// fileChangeSessionExists reports whether file-change history may be served
// for a session. Filetrack retention is independent of session pruning, so
// without this check stale diff content would stay retrievable by URL after
// its session was deleted (until the next GC sweep on store open). When the
// session store is unavailable (sessions disabled), existence cannot be
// verified and history is served as recorded.
func (s *serveServer) fileChangeSessionExists(ctx context.Context, sessionID string) bool {
	if s.store == nil {
		return true
	}
	sess, err := s.store.Get(ctx, sessionID)
	return err == nil && sess != nil
}

// handleSessionFileChanges serves GET /v1/sessions/{id}/file-changes:
// the session's cumulative per-file changes relative to its baseline
// (file state at first touch in the session).
func (s *serveServer) handleSessionFileChanges(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.fileTrackStore()
	if store == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "file tracking is not enabled")
		return
	}
	if !s.fileChangeSessionExists(r.Context(), sessionID) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "session not found")
		return
	}

	changes, err := store.ListSessionChanges(r.Context(), sessionID)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to load file changes")
		return
	}
	if changes == nil {
		changes = []filetrack.CumulativeChange{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"file_changes": changes})
}

// handleSessionFileChangeDiff serves GET /v1/sessions/{id}/file-changes/diff?path=…:
// structured hunks for one file's baseline→current diff, computed from the
// recorded blobs (not live disk, so history stays inspectable after the fact).
func (s *serveServer) handleSessionFileChangeDiff(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.fileTrackStore()
	if store == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "file tracking is not enabled")
		return
	}
	if !s.fileChangeSessionExists(r.Context(), sessionID) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "session not found")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "path query parameter is required")
		return
	}

	content, err := store.GetFileDiffContent(r.Context(), sessionID, path)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to load file diff")
		return
	}
	if content == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "no recorded changes for path")
		return
	}

	hunks := []filetrack.Hunk{}
	if !content.Truncated {
		if built := filetrack.BuildHunks(content.Path, content.Before, content.After); built != nil {
			hunks = built
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      content.Path,
		"kind":      content.Kind,
		"lang":      strings.ToLower(strings.TrimPrefix(filepath.Ext(content.Path), ".")),
		"truncated": content.Truncated,
		"hunks":     hunks,
	})
}
