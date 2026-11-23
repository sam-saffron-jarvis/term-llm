package searchrank

import (
	"context"
	"database/sql"
	"encoding/json"
)

// For a small shortlist, drive the allowed rowids from the session-id index.
// FTS still walks the global posting list to keep BM25 statistics identical,
// but checks membership before grouping or scoring any non-shortlisted row.
const maxScopedSessions = 64

type messageScope string

func (s messageScope) cte() string {
	if s == "" {
		return ""
	}
	return `scoped AS MATERIALIZED (SELECT m.id FROM json_each(?) AS j CROSS JOIN messages m ON m.session_id = j.value), `
}

func (s messageScope) filterSQL() string {
	if s == "" {
		return ""
	}
	return ` AND +rowid IN (SELECT id FROM scoped)`
}

func (s messageScope) bind(args []any) []any {
	if s == "" {
		return args
	}
	return append([]any{string(s)}, args...)
}

func (q Query) collectMessages(ctx context.Context, tx *sql.Tx, candidates map[string]*Candidate, source MessageSource) error {
	var scope messageScope
	if len(candidates) > maxScopedSessions && len(q.terms) == 2 {
		if err := q.seedRequiredTerm(ctx, tx, candidates, source); err != nil {
			return err
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) <= maxScopedSessions {
		ids := make([]string, 0, len(candidates))
		for id := range candidates {
			ids = append(ids, id)
		}
		data, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		scope = messageScope(data)
	}
	if len(q.terms) == 1 || q.strict {
		return q.messages(ctx, tx, candidates, source, scope)
	}
	return q.stagedMessages(ctx, tx, candidates, source, scope)
}

// Both terms of a two-term query are required. If one has few postings, seed
// with that term's hits in either title or visible text. This is lossless even
// when the two terms are in different messages or one is title-only.
func (q Query) seedRequiredTerm(ctx context.Context, tx *sql.Tx, candidates map[string]*Candidate, source MessageSource) error {
	i, n := 0, 0
	for j, term := range q.terms {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, term.match).Scan(&count); err != nil {
			return err
		}
		if j == 0 || count < n {
			i, n = j, count
		}
	}
	if n > 4*maxScopedSessions {
		return nil // too many postings to justify an extra pass
	}
	mask := uint64(1) << i
	rows, err := tx.QueryContext(ctx, `WITH matched AS (SELECT rowid AS message_id FROM messages_fts WHERE messages_fts MATCH ?)
		SELECT m.session_id`+source.joinSQL(), q.terms[i].match)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if c := candidates[id]; c != nil {
			c.coverage |= mask
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, c := range candidates {
		if c.coverage&mask == 0 {
			delete(candidates, id)
		}
	}
	return nil
}
