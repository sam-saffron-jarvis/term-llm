package session

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *SQLiteStore) addMessageAutoSequence(ctx context.Context, sessionID string, msg *Message, partsJSON string) (int64, int, int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, 0, 0, fmt.Errorf("begin immediate transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var maxSeq sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT MAX(sequence) FROM messages WHERE session_id = ?`, sessionID).Scan(&maxSeq); err != nil {
		return 0, 0, 0, fmt.Errorf("get max sequence: %w", err)
	}
	if prefix, ok := ctx.Value(pathNotePrefixKey{}).(int); ok && maxSeq.Valid && maxSeq.Int64 > int64(prefix) {
		return 0, 0, 0, ErrBranchConflict
	}
	sequence := 0
	if maxSeq.Valid {
		sequence = int(maxSeq.Int64) + 1
	}

	id, rev, err := s.insertMessageAndBumpSession(ctx, conn, sessionID, msg, partsJSON, sequence)
	if err != nil {
		return 0, 0, 0, err
	}
	if err := checkpointResponseRunFenceTx(ctx, conn, sessionID, rev); err != nil {
		return 0, 0, 0, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, 0, 0, fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	s.noteOwnTranscriptRev(sessionID, rev)
	return id, sequence, rev, nil
}
