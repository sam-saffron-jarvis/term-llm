package sqlitefts

import (
	"strings"
	"unicode"
)

// LiteralQuery converts free-form user text into an FTS5 MATCH expression that
// treats every whitespace-delimited chunk as a literal string, not as FTS5 query
// syntax. SQL parameters protect the SQL parser; callers still need this helper
// for the second parser: FTS5's MATCH expression grammar.
func LiteralQuery(query string) string {
	return LiteralQueryMin(query, 0)
}

// LiteralQueryMin is like LiteralQuery, but drops chunks shorter than minRunes.
func LiteralQueryMin(query string, minRunes int) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		term := strings.TrimSpace(field)
		if term == "" || runeLen(term) < minRunes {
			continue
		}
		key := strings.ToLower(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, QuoteString(term))
	}
	return strings.Join(terms, " ")
}

// PrefixORQuery converts free-form text into an FTS5 expression matching any
// alphanumeric token by prefix. This is useful for recall-oriented fuzzy-ish
// dedupe searches where any word match should produce a candidate.
func PrefixORQuery(query string, minRunes int) string {
	seen := map[string]struct{}{}
	var terms []string
	for _, field := range strings.Fields(strings.ToLower(query)) {
		var b strings.Builder
		for _, r := range field {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		term := b.String()
		if term == "" || runeLen(term) < minRunes {
			continue
		}
		if _, dup := seen[term]; dup {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, QuoteString(term)+"*")
	}
	return strings.Join(terms, " OR ")
}

// QuoteString returns an FTS5 double-quoted string literal.
func QuoteString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func runeLen(s string) int {
	if s == "" {
		return 0
	}
	return len([]rune(s))
}
