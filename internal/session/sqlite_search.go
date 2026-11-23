package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/term-llm/term-llm/internal/session/searchrank"
)

// Search ranks conversations by title and visible message matches.
func (s *SQLiteStore) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit == 0 {
		opts.Limit = 20
	}

	q := searchrank.Parse(opts.Query, opts.Prefix)
	if q.Highlight == "" {
		return []SearchResult{}, nil
	}

	originCol := "'tui'"
	if s.hasOrigin {
		originCol = "COALESCE(NULLIF(TRIM(s.origin), ''), 'tui')"
	}
	generatedShortCol, generatedLongCol, titleSourceCol := "''", "''", "''"
	if s.hasGeneratedTitles {
		generatedShortCol = "COALESCE(s.generated_short_title, '')"
		generatedLongCol = "COALESCE(s.generated_long_title, '')"
		titleSourceCol = "COALESCE(s.title_source, '')"
	}
	lastMessageAtCol := "NULL"
	if s.hasLastMessageAt {
		lastMessageAtCol = "s.last_message_at"
	}

	projectIDCol := "''"
	projectNameCol := "''"
	projectJoin := ""
	if s.hasProjectID {
		projectIDCol = "COALESCE(s.project_id, '')"
		if s.hasProjectsTable {
			projectNameCol = "COALESCE(p.name, '')"
			projectJoin = " LEFT JOIN projects p ON p.id = s.project_id"
		}
	}

	filterClause, args := s.filterCreatedBy("", nil, opts.CreatedBy)
	categoryClause, categoryArgs := s.sessionCategoryFilter(opts.Categories)
	filterClause += categoryClause
	args = append(args, categoryArgs...)
	if !opts.Archived {
		filterClause += " AND s.archived = FALSE"
	}
	if opts.ExcludeSubagents {
		filterClause += " AND s.parent_id IS NULL"
	}
	if opts.ProjectID != "" {
		if !s.hasProjectID {
			return []SearchResult{}, nil
		}
		filterClause += " AND s.project_id = ?"
		args = append(args, opts.ProjectID)
	}
	// Candidate ranking and result hydration use the same snapshot. Search also
	// works on older read-only databases without the optional search index.
	tx, err := s.queryDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin search: %w", err)
	}
	defer tx.Rollback()
	matches, err := q.Collect(ctx, tx, `SELECT s.id, COALESCE(s.name, ''), COALESCE(s.summary, ''), `+generatedShortCol+`, `+generatedLongCol+`, `+lastMessageAtCol+`, s.created_at
		FROM sessions s WHERE 1=1`+filterClause, args, s.searchMessageSource(), searchTitles, string(opts.Sort), opts.Limit)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	refs, err := json.Marshal(matches)
	if err != nil {
		return nil, fmt.Errorf("encode search matches: %w", err)
	}
	snippet, hydrateArgs := q.SnippetSQL(opts.OmitSnippet, string(refs))
	rows, err := tx.QueryContext(ctx, `
		SELECT `+s.createdByColumn("s")+`, s.id, s.number, COALESCE(m.id, 0), COALESCE(s.name, ''), COALESCE(s.summary, ''),
		       `+generatedShortCol+`, `+generatedLongCol+`, `+titleSourceCol+`,
		       `+snippet+`,
		       s.provider, COALESCE(s.provider_key, ''), s.model, COALESCE(s.mode, ''), `+originCol+`, s.archived,
		       `+s.pinnedSelectCols("s")+`, COALESCE(s.status, ''), `+s.conversationMessageCountSelectSQL("s")+`, `+projectIDCol+`, `+projectNameCol+`,
		       s.created_at, s.updated_at, `+lastMessageAtCol+`, m.created_at
		FROM json_each(?) sm
		JOIN sessions s ON s.id = sm.value->>'session_id'`+projectJoin+`
		LEFT JOIN messages m ON m.id = sm.value->>'message_id'
		ORDER BY sm.key`, hydrateArgs...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var number, pinOrder sql.NullInt64
		var lastMessageAt, messageCreatedAt sql.NullTime
		err := rows.Scan(&r.CreatedBy, &r.SessionID, &number, &r.MessageID, &r.SessionName, &r.Summary,
			&r.GeneratedShortTitle, &r.GeneratedLongTitle, &r.TitleSource, &r.Snippet, &r.Provider, &r.ProviderKey,
			&r.Model, &r.Mode, &r.Origin, &r.Archived, &r.Pinned, &pinOrder, &r.Status, &r.MessageCount, &r.ProjectID, &r.ProjectName,
			&r.SessionCreatedAt, &r.UpdatedAt, &lastMessageAt, &messageCreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.PinOrder = pinOrderValue(r.Pinned, pinOrder)
		r.SessionNumber = number.Int64
		r.LastMessageAt = lastMessageAt.Time
		r.CreatedAt = messageCreatedAt.Time
		results = append(results, r)
	}
	return results, rows.Err()
}

func searchTitles(name, summary, short, long string) (string, string) {
	s := SessionSummary{Name: name, Summary: summary, GeneratedShortTitle: short, GeneratedLongTitle: long}
	return s.PreferredShortTitle(), s.PreferredLongTitle()
}
