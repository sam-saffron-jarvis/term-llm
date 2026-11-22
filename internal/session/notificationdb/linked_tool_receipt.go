package notificationdb

import (
	"context"
	"database/sql"
	"errors"

	"github.com/term-llm/term-llm/internal/sqliteutil"
)

const LinkedToolReceiptSchemaV72 = `
CREATE TABLE IF NOT EXISTS linked_tool_receipts (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    client_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    response_id TEXT NOT NULL REFERENCES serve_response_lifecycle(response_id) ON DELETE CASCADE,
    PRIMARY KEY (session_id, device_id, client_key)
);
CREATE INDEX IF NOT EXISTS linked_tool_receipts_response ON linked_tool_receipts(response_id);
`

func MigrateLinkedToolReceiptV72(db sqliteutil.Executor) error {
	_, err := db.Exec(LinkedToolReceiptSchemaV72)
	return err
}

type LinkedToolClaim struct {
	DeviceID, Key, Fingerprint string
}

type LinkedToolReceipt struct {
	ResponseID, Fingerprint string
	State                   ResponseRunState
}

var ErrLinkedToolClaimExists = errors.New("linked tool handoff already admitted")

// ClaimLinkedToolTx records acceptance in the same transaction that starts the
// run. A duplicate can never schedule provider work, even across processes.
func ClaimLinkedToolTx(ctx context.Context, tx *sql.Tx, sessionID, responseID string, claim *LinkedToolClaim) error {
	if claim == nil {
		return nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO linked_tool_receipts(session_id,device_id,client_key,fingerprint,response_id)
		VALUES(?,?,?,?,?) ON CONFLICT(session_id,device_id,client_key) DO NOTHING`, sessionID, claim.DeviceID, claim.Key, claim.Fingerprint, responseID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrLinkedToolClaimExists
	}
	return nil
}

func ReadLinkedToolReceipt(ctx context.Context, db QueryExecer, sessionID, deviceID, key string) (LinkedToolReceipt, error) {
	var receipt LinkedToolReceipt
	err := db.QueryRowContext(ctx, `SELECT r.response_id,r.fingerprint,l.state FROM linked_tool_receipts r
		JOIN serve_response_lifecycle l ON l.response_id=r.response_id
		WHERE r.session_id=? AND r.device_id=? AND r.client_key=?`, sessionID, deviceID, key).Scan(&receipt.ResponseID, &receipt.Fingerprint, &receipt.State)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return receipt, err
}
