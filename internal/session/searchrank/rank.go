// Package searchrank ranks sessions by title and conversation matches.
package searchrank

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/term-llm/term-llm/internal/sqlitefts"
)

// Bound the number of FTS scans and the coverage mask. Longer queries still
// work, but require all terms together in a message or in a title.
const maxRankedSearchTerms = 12

type sessionSearchTerm struct {
	text  string // normalized title tokens, separated and surrounded by spaces
	match string // escaped FTS phrase, optionally with a final-token prefix
}

// Query holds the distinct search terms and their escaped FTS expression.
type Query struct {
	terms     []sessionSearchTerm
	Highlight string // union of the terms, used to highlight the chosen excerpt
	strict    bool
	prefix    bool
	phrase    string
}

// Parse builds literal title and FTS terms from a free-form search query.
func Parse(text string, allowPrefix bool) Query {
	var q Query
	seen := make(map[string]bool)
	var activeTerm string
	var ordered strings.Builder
	for _, field := range strings.Fields(text) {
		activeTerm = ""
		words := searchTitleTokens(field)
		if len(words) == 0 {
			continue
		}
		literal := strings.Join(words, " ")
		term := " " + literal + " "
		if !seen[term] {
			seen[term] = true
			activeTerm = field
			ordered.WriteString(strings.TrimSuffix(term, " "))
			q.terms = append(q.terms, sessionSearchTerm{term, sqlitefts.QuoteString(literal)})
		}
	}
	if len(q.terms) > maxRankedSearchTerms {
		q.strict = true
		q.Highlight = strings.Join(q.ftsTerms(), " ")
		q.phrase = ordered.String() + " "
		return q
	}
	if n := len(q.terms); allowPrefix && n > 1 && activeTerm != "" && !hasTrailingSpace(text) {
		last := &q.terms[n-1]
		word := strings.TrimSpace(last.text)
		// While typing, expand a substantial final word, not e.g. CI, an ID,
		// or a multi-token literal such as src/main.go.
		if utf8.RuneCountInString(word) >= 4 && strings.IndexFunc(activeTerm, func(r rune) bool { return !unicode.IsLetter(r) && !unicode61Diacritic(r) }) < 0 {
			q.prefix = true
			last.match += "*"
		}
	}
	q.Highlight = strings.Join(q.ftsTerms(), " OR ")
	q.phrase = ordered.String()
	if !q.prefix {
		q.phrase += " "
	}
	return q
}

func (q Query) ftsTerms() []string {
	parts := make([]string, len(q.terms))
	for i, term := range q.terms {
		parts[i] = term.match
	}
	return parts
}

func hasTrailingSpace(text string) bool {
	r, _ := utf8.DecodeLastRuneInString(text)
	return unicode.IsSpace(r)
}

func (q Query) titleMatch(title string) (coverage uint64, phrase bool) {
	if len(q.terms) == 0 || title == "" {
		return 0, false
	}
	tokens := " " + strings.Join(searchTitleTokens(title), " ") + " "
	for i, term := range q.terms {
		needle := term.text
		if q.prefix && i == len(q.terms)-1 {
			needle = strings.TrimSuffix(needle, " ")
		}
		if strings.Contains(tokens, needle) {
			if !q.strict {
				coverage |= 1 << i
			}
		} else if q.strict {
			return 0, false
		}
	}
	if q.strict {
		coverage = 1
	}
	return coverage, strings.Contains(tokens, q.phrase)
}

// Candidate identifies a ranked session and its best source of match evidence.
type Candidate struct {
	SessionID string `json:"session_id"`
	MessageID int64  `json:"message_id,omitempty"`
	Title     string `json:"title,omitempty"` // only used when no message matched
	activity  time.Time
	coverage  uint64
	titleBits uint64
	phrase    bool
	bestTerms int
	bestScore float64
}

// MessageSource specifies the session store's visible-message predicate and
// optional covering index. Both are trusted SQL from the store's schema.
type MessageSource struct {
	Visible string
	Index   string
}

func (s MessageSource) joinSQL() string {
	index := ""
	if s.Index != "" {
		index = " INDEXED BY " + s.Index
	}
	return " FROM matched CROSS JOIN messages m" + index + " ON m.id = matched.message_id WHERE " + s.Visible
}

const userMessageWeight = 1.2

func (c *Candidate) consider(id int64, terms int, score float64) {
	if terms > c.bestTerms || (terms == c.bestTerms && (c.MessageID == 0 || score > c.bestScore || (score == c.bestScore && id < c.MessageID))) {
		c.bestTerms, c.bestScore, c.MessageID = terms, score, id
	}
}

func (q Query) minimum() int {
	if q.strict {
		return 1
	}
	if len(q.terms) > 2 {
		return len(q.terms) - 1 // tolerate a missing term, but not weak OR-only hits
	}
	return len(q.terms)
}

// Collect ranks the selected sessions. titlesSQL returns id, name, summary,
// short and long titles, last-message time, and creation time, in that order.
func (q Query) Collect(ctx context.Context, tx *sql.Tx, titlesSQL string, args []any, source MessageSource, preferredTitles func(name, summary, short, long string) (string, string), sort string, limit int) ([]*Candidate, error) {
	candidates, err := q.titles(ctx, tx, titlesSQL, args, preferredTitles)
	if err != nil {
		return nil, fmt.Errorf("search titles: %w", err)
	}
	if err := q.collectMessages(ctx, tx, candidates, source); err != nil {
		return nil, fmt.Errorf("rank matching messages: %w", err)
	}
	return q.rank(candidates, sort, limit)
}

func (q Query) titles(ctx context.Context, tx *sql.Tx, query string, args []any, preferredTitles func(string, string, string, string) (string, string)) (map[string]*Candidate, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make(map[string]*Candidate)
	for rows.Next() {
		var c Candidate
		var name, summary, short, long string
		var activity sql.NullTime
		if err := rows.Scan(&c.SessionID, &name, &summary, &short, &long, &activity, &c.activity); err != nil {
			return nil, err
		}
		if activity.Valid {
			c.activity = activity.Time
		}
		short, long = preferredTitles(name, summary, short, long)
		shortBits, shortPhrase := q.titleMatch(short)
		c.Title, c.titleBits, c.phrase = short, shortBits, shortPhrase
		if long != short {
			longBits, longPhrase := q.titleMatch(long)
			if (longPhrase && !shortPhrase) || bits.OnesCount64(longBits) > bits.OnesCount64(shortBits) {
				c.Title = long
			}
			c.titleBits |= longBits
			c.phrase = c.phrase || longPhrase
		}
		c.coverage = c.titleBits
		candidates[c.SessionID] = &c
	}
	return candidates, rows.Err()
}

func (q Query) messages(ctx context.Context, tx *sql.Tx, candidates map[string]*Candidate, source MessageSource, scope messageScope) error {
	terms := q.terms
	if q.strict {
		terms = []sessionSearchTerm{{match: q.Highlight}}
	}
	parts := make([]string, len(terms))
	args := make([]any, 0, len(terms)*2)
	for i, term := range terms {
		parts[i] = `SELECT rowid AS message_id, ? AS term_bit, rank FROM messages_fts WHERE messages_fts MATCH ?` + scope.filterSQL()
		args = append(args, 1<<i, term.match)
	}
	// Drive primary-key message lookups from the inverted index, not from a
	// scan of messages. Read only one row per matching message, even when
	// several terms matched; a term contributes once to session coverage.
	rows, err := tx.QueryContext(ctx, `WITH `+scope.cte()+`raw AS (`+strings.Join(parts, " UNION ALL ")+`),
		matched AS (SELECT message_id, SUM(term_bit) AS coverage, COUNT(*) AS terms, SUM(rank) AS score
			FROM raw GROUP BY message_id)
		SELECT m.session_id, m.id, matched.coverage, matched.terms,
		       -matched.score * CASE WHEN m.role = 'user' THEN ? ELSE 1.0 END`+source.joinSQL(), append(scope.bind(args), userMessageWeight)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var mid int64
		var coverage uint64
		var terms int
		var score float64
		if err := rows.Scan(&sid, &mid, &coverage, &terms, &score); err != nil {
			return err
		}
		if c := candidates[sid]; c != nil {
			c.coverage |= coverage
			c.consider(mid, terms, score)
		}
	}
	return rows.Err()
}

func (q Query) rank(candidates map[string]*Candidate, order string, limit int) ([]*Candidate, error) {
	switch order {
	case "", "relevance", "newest", "oldest":
	default:
		return nil, fmt.Errorf("invalid search sort: %q", order)
	}
	minimum := q.minimum()
	var matches []*Candidate
	for _, c := range candidates {
		if bits.OnesCount64(c.coverage) >= minimum {
			matches = append(matches, c)
		}
	}
	slices.SortFunc(matches, func(a, b *Candidate) int {
		switch order {
		case "newest":
			return cmp.Or(b.activity.Compare(a.activity), cmp.Compare(a.SessionID, b.SessionID))
		case "oldest":
			return cmp.Or(a.activity.Compare(b.activity), cmp.Compare(a.SessionID, b.SessionID))
		}
		return cmp.Or(
			cmp.Compare(bits.OnesCount64(b.coverage), bits.OnesCount64(a.coverage)),
			cmp.Compare(phraseRank(b.phrase), phraseRank(a.phrase)),
			cmp.Compare(bits.OnesCount64(b.titleBits), bits.OnesCount64(a.titleBits)),
			cmp.Compare(b.bestTerms, a.bestTerms),
			cmp.Compare(b.bestScore, a.bestScore),
			cmp.Compare(a.MessageID, b.MessageID),
			cmp.Compare(a.SessionID, b.SessionID),
		)
	})
	if limit >= 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	for _, c := range matches {
		if c.MessageID != 0 {
			c.Title = ""
		}
	}
	return matches, nil
}

func phraseRank(phrase bool) int {
	if phrase {
		return 1
	}
	return 0
}

// SnippetSQL projects excerpts using the MATCH expression that selected the hit.
// A caller that doesn't display excerpts can skip all of those FTS lookups.
func (q Query) SnippetSQL(omit bool, refs string) (string, []any) {
	if omit {
		return "''", []any{refs}
	}
	return `COALESCE(CASE WHEN m.id IS NOT NULL THEN (SELECT snippet(messages_fts, 0, '**', '**', '...', 32) FROM messages_fts
	                 WHERE rowid = m.id AND messages_fts MATCH ?) END, sm.value->>'title', '')`, []any{q.Highlight, refs}
}
