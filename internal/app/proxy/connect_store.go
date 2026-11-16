package proxy

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const connectSchema = `
CREATE TABLE connect_requests (
 id TEXT PRIMARY KEY, secret_hash TEXT NOT NULL, peer TEXT NOT NULL,
 status TEXT NOT NULL, expires INTEGER NOT NULL, retained_until INTEGER NOT NULL,
 result_expires INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL
);
CREATE INDEX connect_requests_peer ON connect_requests(peer,status,expires);
CREATE TABLE connect_managed (
 account TEXT PRIMARY KEY, route TEXT UNIQUE NOT NULL, prefix TEXT UNIQUE NOT NULL,
 authority TEXT NOT NULL, spec TEXT NOT NULL, request_id TEXT NOT NULL
);`

const (
	connectLifetime       = 5 * time.Minute
	connectRetention      = 24 * time.Hour
	connectResultLifetime = 5 * time.Minute
	connectPendingLimit   = 64
	connectPeerLimit      = 16
	connectReceiptLimit   = 512
)

type connectRequest struct {
	ID             string         `json:"id"`
	Code           string         `json:"code"`
	Origin         string         `json:"origin"`
	Expires        int64          `json:"expires"`
	Status         string         `json:"status"`
	Action         string         `json:"action"`
	Account        string         `json:"account"`
	ClientName     string         `json:"client_name"`
	Route          string         `json:"route"`
	Prefix         string         `json:"prefix"`
	ResolvedAction string         `json:"resolved_action,omitempty"`
	SourceAccount  string         `json:"source_account,omitempty"`
	Result         *connectResult `json:"result,omitempty"`
}
type connectResult struct {
	Token      string `json:"token,omitempty"`
	CallerID   string `json:"caller_id,omitempty"`
	GatewayURL string `json:"gateway_url"`
	Account    string `json:"account"`
	Route      string `json:"route"`
}
type connectProof struct {
	SourceAccount   string `json:"source_account,omitempty"`
	SourceRoute     string `json:"source_route,omitempty"`
	SourceAuthority string `json:"source_authority,omitempty"`
	SourceSpec      string `json:"source_spec,omitempty"`
	AddRoute        bool   `json:"add_route,omitempty"`
	Adopt           bool   `json:"adopt,omitempty"`
	Action          string `json:"action"`
	Generation      int64  `json:"generation"`
	Authority       string `json:"authority"`
	Spec            string `json:"spec"`
	Revision        int64  `json:"revision"`
}
type connectReceipt struct {
	connectRequest
	Proof                   connectProof `json:"proof"`
	hash, peer              string
	retained, resultExpires int64
}
type connectProblem struct {
	status  int
	message string
}

func (e connectProblem) Error() string { return e.message }
func connectConflict() error {
	return connectProblem{409, "Approved connection changed or conflicts; start a new request explicitly"}
}

func pruneConnect(tx *sql.Tx, now int64) error {
	for _, statement := range []string{
		"DELETE FROM connect_requests WHERE retained_until<=?",
		"UPDATE connect_requests SET status='expired' WHERE status IN ('pending','approved') AND expires<=?",
		"UPDATE connect_requests SET data=json_remove(data,'$.result.token'),result_expires=0 WHERE result_expires>0 AND result_expires<=?",
	} {
		if _, err := tx.Exec(statement, now); err != nil {
			return err
		}
	}
	return nil
}
func readConnect(tx *sql.Tx, id string) (connectReceipt, error) {
	var record connectReceipt
	var data, status string
	var expires int64
	err := tx.QueryRow("SELECT secret_hash,peer,status,expires,retained_until,result_expires,data FROM connect_requests WHERE id=?", id).Scan(&record.hash, &record.peer, &status, &expires, &record.retained, &record.resultExpires, &data)
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal([]byte(data), &record); err != nil {
		return record, err
	}
	record.Status = status
	record.Expires = expires
	record.ID = id
	return record, nil
}
func writeConnect(tx *sql.Tx, record connectReceipt) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE connect_requests SET status=?,result_expires=?,data=? WHERE id=?", record.Status, record.resultExpires, string(data), record.ID)
	return err
}
func authorizedConnect(tx *sql.Tx, id, secret, origin string) (connectReceipt, error) {
	record, err := readConnect(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return record, connectProblem{401, "Connect capability unavailable"}
	}
	if err != nil {
		return record, err
	}
	if record.Origin != origin || subtle.ConstantTimeCompare([]byte(record.hash), []byte(hashToken(secret))) != 1 {
		return record, connectProblem{401, "Connect capability unavailable"}
	}
	return record, nil
}
func (s *Service) connectTransaction(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.store.accountWrite(ctx, func(tx *sql.Tx) error {
		if err := pruneConnect(tx, time.Now().Unix()); err != nil {
			return err
		}
		return fn(tx)
	})
}

func marshalConnect(record connectReceipt) (string, error) {
	data, err := json.Marshal(record)
	return string(data), err
}

// retainConnect lets the host's existing retention tick clean idle private
// receipt results too. Every connect operation also applies these same bounds.
func (s *store) retainConnect(ctx context.Context) error {
	return s.accountWrite(ctx, func(tx *sql.Tx) error { return pruneConnect(tx, time.Now().Unix()) })
}

// Under pressure, only discard authority-free tombstones after their capability
// lifetime ended. An evicted id may start again, but gets a new code and must
// receive fresh approval. Completed or ever-approved receipts are never victims.
func evictConnectTombstones(tx *sql.Tx, needed int) (int64, error) {
	result, err := tx.Exec(`DELETE FROM connect_requests WHERE id IN (
 SELECT id FROM connect_requests WHERE status IN ('expired','canceled')
 AND expires<=? AND COALESCE(json_extract(data,'$.resolved_action'),'')=''
 ORDER BY expires,id LIMIT ?)`, time.Now().Unix(), needed)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
