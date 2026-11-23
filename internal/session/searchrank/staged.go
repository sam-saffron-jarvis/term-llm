package searchrank

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/bits"
	"strings"
)

type messageEvidence struct {
	candidate *Candidate
	id        int64
	coverage  uint64
	user      bool
}

// stagedMessages uses the same term coverage and BM25 tie-break as the
// one-pass query. A message can't win if its session doesn't qualify or if a
// different message in that session covers more terms.
func (q Query) stagedMessages(ctx context.Context, tx *sql.Tx, candidates map[string]*Candidate, source MessageSource, scope messageScope) error {
	evidence, err := q.collectEvidence(ctx, tx, candidates, source, scope)
	if err != nil {
		return err
	}
	var finalists []messageEvidence
	idsByTerm := make([][]int64, len(q.terms))
	minimum := q.minimum()
	for _, e := range evidence {
		if bits.OnesCount64(e.candidate.coverage) < minimum || bits.OnesCount64(e.coverage) != e.candidate.bestTerms {
			continue
		}
		finalists = append(finalists, e)
		for mask := e.coverage; mask != 0; mask &= mask - 1 {
			i := bits.TrailingZeros64(mask)
			idsByTerm[i] = append(idsByTerm[i], e.id)
		}
	}
	// Sum float64 scores in query-term order, just like the one-pass UNION.
	scores := make(map[int64]float64, len(finalists))
	for i, ids := range idsByTerm {
		if len(ids) > 0 {
			if err := scoreMessages(ctx, tx, q.terms[i].match, ids, scores); err != nil {
				return err
			}
		}
	}
	for _, e := range finalists {
		score := -scores[e.id]
		if e.user {
			score *= userMessageWeight
		}
		e.candidate.consider(e.id, e.candidate.bestTerms, score)
	}
	return nil
}

func (q Query) collectEvidence(ctx context.Context, tx *sql.Tx, candidates map[string]*Candidate, source MessageSource, scope messageScope) ([]messageEvidence, error) {
	parts := make([]string, len(q.terms))
	args := make([]any, 0, len(q.terms)*2)
	for i, term := range q.terms {
		parts[i] = `SELECT rowid AS message_id, ? AS term_bit FROM messages_fts WHERE messages_fts MATCH ?` + scope.filterSQL()
		args = append(args, 1<<i, term.match)
	}
	rows, err := tx.QueryContext(ctx, `WITH `+scope.cte()+`raw AS (`+strings.Join(parts, " UNION ALL ")+`),
		matched AS (SELECT message_id, SUM(term_bit) AS coverage FROM raw GROUP BY message_id)
		SELECT m.session_id, m.id, matched.coverage, m.role = 'user'`+source.joinSQL(), scope.bind(args)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evidence []messageEvidence
	for rows.Next() {
		var sid string
		var e messageEvidence
		if err := rows.Scan(&sid, &e.id, &e.coverage, &e.user); err != nil {
			return nil, err
		}
		if c := candidates[sid]; c != nil {
			e.candidate = c
			c.coverage |= e.coverage
			c.bestTerms = max(c.bestTerms, bits.OnesCount64(e.coverage))
			evidence = append(evidence, e)
		}
	}
	return evidence, rows.Err()
}

func scoreMessages(ctx context.Context, tx *sql.Tx, term string, ids []int64, scores map[int64]float64) error {
	data, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode scoring shortlist: %w", err)
	}
	// Unary + keeps the IN constraint out of the FTS planner. With a bare
	// rowid it may reopen MATCH for each shortlist ID, especially for a
	// prefix. Walk the term once and evaluate BM25 only for shortlisted hits.
	rows, err := tx.QueryContext(ctx, `SELECT rowid, bm25(messages_fts) FROM messages_fts
		WHERE messages_fts MATCH ? AND +rowid IN (SELECT value FROM json_each(?))`, term, string(data))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return err
		}
		scores[id] += score
	}
	return rows.Err()
}
