package notificationdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/sqliteutil"
)

// The row follows its exact transcript anchor rather than the response stream's
// retention window. Copied/branched messages do not inherit this grant.
const LinkedToolHandoffSchemaV71 = `
CREATE TABLE IF NOT EXISTS linked_tool_handoffs (
    message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    response_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    pending_call_ids TEXT NOT NULL
);
`

func MigrateLinkedToolHandoffV71(db sqliteutil.Executor) error {
	_, err := db.Exec(LinkedToolHandoffSchemaV71)
	return err
}

func RecordLinkedToolHandoffTx(ctx context.Context, tx *sql.Tx, sessionID string, terminal ResponseRunTerminal) error {
	if terminal.Outcome != ResponseRunCompleted || terminal.LinkedToolHandoffMessageID <= 0 || terminal.LinkedDeviceID == "" || len(terminal.PendingClientCallIDs) == 0 {
		return nil
	}
	ids, err := json.Marshal(terminal.PendingClientCallIDs)
	if err != nil {
		return fmt.Errorf("encode linked tool handoff: %w", err)
	}
	// Only the fenced terminal transition can stamp the owning run's assistant
	// row. A racing transcript change or a turn without a cursor grants nothing.
	_, err = tx.ExecContext(ctx, `INSERT INTO linked_tool_handoffs(message_id,response_id,device_id,pending_call_ids)
		SELECT id,response_id,?,? FROM messages WHERE id=? AND session_id=? AND response_id=? AND role='assistant' AND COALESCE(compaction_tail,FALSE)=FALSE`,
		terminal.LinkedDeviceID, string(ids), terminal.LinkedToolHandoffMessageID, sessionID, terminal.ResponseID)
	if err != nil {
		return fmt.Errorf("persist linked tool handoff: %w", err)
	}
	return nil
}

// PendingLinkedToolHandoff verifies that the peer owns the current assistant
// turn and that the calls recorded at completion still exist in the transcript.
func PendingLinkedToolHandoff(ctx context.Context, db QueryExecer, messageID int64, deviceID string) ([]string, error) {
	var partsJSON, idsJSON string
	err := db.QueryRowContext(ctx, `SELECT m.parts,h.pending_call_ids FROM linked_tool_handoffs h
		JOIN messages m ON m.id=h.message_id AND m.response_id=h.response_id
		WHERE h.message_id=? AND h.device_id=? AND m.role='assistant' AND COALESCE(m.compaction_tail,FALSE)=FALSE
		AND NOT EXISTS (SELECT 1 FROM messages newer WHERE newer.session_id=m.session_id
			AND newer.sequence>=m.sequence AND (newer.sequence>m.sequence OR newer.id>m.id)
			AND newer.role IN ('user','assistant','tool') AND COALESCE(newer.compaction_tail,FALSE)=FALSE)`, messageID, deviceID).Scan(&partsJSON, &idsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read linked tool handoff: %w", err)
	}
	var parts []core.Part
	if err := json.Unmarshal([]byte(partsJSON), &parts); err != nil {
		return nil, fmt.Errorf("decode linked tool handoff message: %w", err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
		return nil, fmt.Errorf("decode linked tool handoff call IDs: %w", err)
	}
	calls := make(map[string]bool, len(parts))
	for _, part := range parts {
		if part.Type == core.PartToolCall && part.ToolCall != nil {
			calls[part.ToolCall.ID] = true
		}
	}
	for _, id := range ids {
		if id == "" || !calls[id] {
			return nil, nil
		}
		delete(calls, id)
	}
	return ids, nil
}
