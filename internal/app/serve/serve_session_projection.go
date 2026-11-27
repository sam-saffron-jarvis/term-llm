package serve

import (
	"strings"

	appruntime "github.com/term-llm/term-llm/internal/app/runtime"
	"github.com/term-llm/term-llm/internal/session"
)

func (s *serveServer) webSessionEntryFromSummary(sess session.SessionSummary) webSessionEntry {
	return webSessionEntry{
		createdByID:           sess.CreatedBy,
		BranchParentSessionID: sess.BranchParentSessionID,
		BranchParentNumber:    sess.BranchParentNumber,
		Name:                  sess.Name,
		ID:                    sess.ID,
		Number:                sess.Number,
		ShortTitle:            sess.PreferredShortTitle(),
		LongTitle:             sess.PreferredLongTitle(),
		Mode:                  sess.Mode,
		Origin:                sess.Origin,
		Provider:              sessionSummaryProviderKey(s.cfgRef, sess),
		Model:                 sess.Model,
		Agent:                 sess.Agent,
		ProjectID:             sess.ProjectID,
		ProjectName:           sess.ProjectName,
		CWD:                   sess.CWD,
		WorktreeDir:           sess.WorktreeDir,
		Archived:              sess.Archived,
		Pinned:                sess.Pinned,
		PinOrder:              sess.PinOrder,
		CreatedAt:             sess.CreatedAt.UnixMilli(),
		LastMessageAt:         sessionSummaryLastMessageAt(sess).UnixMilli(),
		MsgCount:              sess.MessageCount,
	}
}

func (s *serveServer) webSessionEntryFromSession(sess *session.Session) webSessionEntry {
	lastMessageAt := sess.UpdatedAt
	if lastMessageAt.IsZero() {
		lastMessageAt = sess.CreatedAt
	}
	provider := strings.TrimSpace(sess.ProviderKey)
	if provider == "" {
		provider = appruntime.ResolveSessionProviderKey(s.cfgRef, sess)
	}
	return webSessionEntry{
		createdByID:           sess.CreatedBy,
		BranchParentSessionID: sess.BranchParentSessionID,
		BranchParentNumber:    sess.BranchParentNumber,
		Name:                  sess.Name,
		ID:                    sess.ID,
		Number:                sess.Number,
		ShortTitle:            sess.PreferredShortTitle(),
		LongTitle:             sess.PreferredLongTitle(),
		Mode:                  sess.Mode,
		Origin:                sess.Origin,
		Provider:              provider,
		Model:                 sess.Model,
		Agent:                 sess.Agent,
		ProjectID:             sess.ProjectID,
		ProjectName:           sess.ProjectName,
		CWD:                   sess.CWD,
		WorktreeDir:           sess.WorktreeDir,
		Archived:              sess.Archived,
		Pinned:                sess.Pinned,
		PinOrder:              sess.PinOrder,
		CreatedAt:             sess.CreatedAt.UnixMilli(),
		LastMessageAt:         lastMessageAt.UnixMilli(),
		MsgCount:              sess.MessageCount,
		ParentSessionID:       sess.ParentID,
		Delegated:             sessionIsDelegatedChild(sess),
	}
}
