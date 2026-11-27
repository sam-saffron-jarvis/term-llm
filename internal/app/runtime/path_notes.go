package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/session"
)

// GenerateBranchPathNote runs the isolated helper and accounts usage to its source.
func GenerateBranchPathNote(ctx context.Context, store session.Store, sourceSessionID string, source []core.Message, mode string, config engine.PathNotesConfig, providerFor func(*session.Session) (core.Provider, error), record func(string, core.Usage)) (*session.BranchPathNote, int, string) {
	if mode == "" || mode == "none" || mode == "clean" || len(source) == 0 {
		return nil, 0, ""
	}
	sess, err := store.Get(ctx, sourceSessionID)
	if err != nil || sess == nil {
		return nil, http.StatusBadRequest, "branch source was not found"
	}
	provider, err := providerFor(sess)
	if err != nil || provider == nil {
		return nil, http.StatusConflict, "branch context generation is unavailable"
	}
	notes, err := engine.GeneratePathNotes(ctx, provider, sess.Model, source, config)
	if notes != nil && !notes.Usage.BillableCountersZero() {
		record(sess.Model, notes.Usage)
		RecordStoreHelperUsage(context.WithoutCancel(ctx), store, sourceSessionID, sess.Model, session.ModelUsageHandover, notes.Usage)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, http.StatusGatewayTimeout, "branch context generation timed out"
		}
		if errors.Is(err, context.Canceled) {
			return nil, http.StatusConflict, "branch context generation was cancelled"
		}
		return nil, http.StatusBadGateway, "failed to prepare branch context"
	}
	if notes == nil || strings.TrimSpace(notes.Notes) == "" {
		return nil, 0, ""
	}
	return &session.BranchPathNote{
		Text: notes.Notes,
		Provenance: core.PathNoteProvenance{
			SourceMessages:  notes.SourceMessages,
			OmittedMessages: notes.OmittedMessages,
			ReadFiles:       notes.ReadFiles,
			ModifiedFiles:   notes.ModifiedFiles,
			Model:           notes.Model,
			Focus:           config.Focus,
		},
	}, 0, ""
}
