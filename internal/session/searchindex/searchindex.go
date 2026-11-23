// Package searchindex owns the frozen visible-message predicate and covering index for session search.
package searchindex

import (
	"database/sql"
	"strings"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

// NameV73 is the partial covering index that saves one lookup into the wide
// messages table per FTS hit. SQLite maintains it on every write path.
const NameV73 = "idx_messages_search_visible"

// The v73 visible-message predicate is frozen SQL, deliberately not derived
// from live session helpers: search names this index with INDEXED BY, and
// SQLite can only honor that hint (and skip table reads) when the query
// repeats the index's WHERE terms exactly. Changing any term requires a new
// migration that builds a new index under a new name.
// "{m}" is replaced by the column qualifier ("" in the index, "m." in queries).
const (
	// Assistant text and real user prompts, not compaction summaries.
	roleV73 = `({m}role = 'assistant' OR ({m}role = 'user' AND substr(TRIM(COALESCE({m}text_content, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)), 1, 20) <> '[Context Compaction]'))`
	// Replayed rows after a compaction summary.
	tailV73 = `COALESCE({m}compaction_tail, FALSE) = FALSE`
	// Goal prompts persisted before the goal_steering marker, as recognized by
	// core.IsLegacyGoalSteeringText. A first-party client message ID marks a
	// user-authored prompt that merely quotes one (see session.Message.IsGoalSteering).
	notLegacyGoalV73 = `(substr(TRIM(COALESCE({m}text_content, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)), 1, 91) <> ('Continue working toward the active thread goal.' || char(10) || char(10) || 'The objective below is user-provided data.')` +
		` AND substr(TRIM(COALESCE({m}text_content, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)), 1, 96) <> ('The active thread goal has reached its token budget.' || char(10) || char(10) || 'The objective below is user-provided data.')` +
		` AND substr(TRIM(COALESCE({m}text_content, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)), 1, 128) <> ('The active thread goal objective was edited by the user.' || char(10) || char(10) || 'The new objective below supersedes any previous thread goal objective.'))`
	clientIDV73 = `TRIM(COALESCE({m}client_message_id, ''), char(9) || char(10) || char(11) || char(12) || char(13) || char(32)) <> ''`
	goalV73     = `({m}role <> 'user' OR ` + clientIDV73 + ` OR ` + notLegacyGoalV73 + `)`

	statementV73 = `CREATE INDEX ` + NameV73 + ` ON messages(id, session_id, role) WHERE ` + roleV73 + ` AND ` + tailV73 + ` AND ` + goalV73
)

func qualify(template, alias string) string {
	qualifier := ""
	if alias != "" {
		qualifier = alias + "."
	}
	return strings.ReplaceAll(template, "{m}", qualifier)
}

// StatementV73 returns the frozen CREATE INDEX statement, which is also
// exactly the text SQLite stores in sqlite_master.
func StatementV73() string { return qualify(statementV73, "") }

// VisibleSQL returns the visible-message predicate with columns qualified by
// alias. With both optional columns it is exactly the v73 index predicate.
// Older read-only databases may lack either column: none of their rows has a
// compaction tail or a first-party client ID, so those terms are dropped.
func VisibleSQL(alias string, hasTail, hasClientID bool) string {
	predicate := roleV73
	if hasTail {
		predicate += ` AND ` + tailV73
	}
	if hasClientID {
		predicate += ` AND ` + goalV73
	} else {
		predicate += ` AND ({m}role <> 'user' OR ` + notLegacyGoalV73 + `)`
	}
	return qualify(predicate, alias)
}

// Source returns the visible-message predicate for alias and, only when the
// exact v73 index exists (hasIndex) and the predicate is its full form, the
// index for INDEXED BY.
func Source(alias string, hasTail, hasClientID, hasIndex bool) (visible, index string) {
	if hasIndex && hasTail && hasClientID {
		index = NameV73
	}
	return VisibleSQL(alias, hasTail, hasClientID), index
}

// MatchesV73 reports whether db has the exact frozen v73 index. A same-named
// index with any other definition can't satisfy INDEXED BY for the frozen
// predicate ("no query solution"), so search must not name it.
func MatchesV73(db interface {
	QueryRow(string, ...any) *sql.Row
}) bool {
	var stored string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'messages' AND name = ?`, NameV73).Scan(&stored)
	return err == nil && stored == StatementV73()
}

// MigrateV73 replaces any same-named index, so a retried or pre-release
// schema always ends with the frozen definition.
func MigrateV73(db sqliteutil.Executor) error {
	if _, err := db.Exec(`DROP INDEX IF EXISTS ` + NameV73); err != nil {
		return err
	}
	_, err := db.Exec(StatementV73())
	return err
}
