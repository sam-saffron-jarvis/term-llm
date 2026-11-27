package branchtree

import (
	"iter"
	"math"
)

// TurnEndSQL extends a fork anchor sequence over the tool rows persisted
// directly after it. CLI providers record a turn's text before its tool
// results, so copying only through the anchor would orphan those results and
// the media they publish. Arguments: anchor sequence, session ID, anchor
// sequence, session ID, anchor sequence.
const TurnEndSQL = `SELECT COALESCE(MAX(sequence), ?) FROM messages WHERE session_id = ? AND sequence > ? AND role = 'tool'
	AND sequence < COALESCE((SELECT MIN(sequence) FROM messages WHERE session_id = ? AND sequence > ? AND role <> 'tool'), 9223372036854775807)`

// TurnEnd applies TurnEndSQL to loaded rows given as (sequence, isTool) pairs.
func TurnEnd(anchor int, rows iter.Seq2[int, bool]) int {
	next, end := math.MaxInt, anchor
	for seq, tool := range rows {
		if seq > anchor && !tool && seq < next {
			next = seq
		}
	}
	for seq, tool := range rows {
		if seq > end && seq < next && tool {
			end = seq
		}
	}
	return end
}
