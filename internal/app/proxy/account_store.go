package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const accountSchema = `CREATE TABLE accounts (name TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL, generation INTEGER NOT NULL, expires INTEGER NOT NULL, updated INTEGER NOT NULL, data TEXT NOT NULL);`

// AccountInfo is the entire public account representation. Credentials, identity
// IDs, fixture endpoints and bootstrap paths must never be exported.
type AccountInfo struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	AuthMode   string `json:"auth_mode,omitempty"`
	State      string `json:"state"`
	Generation int64  `json:"generation"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	Updated    int64  `json:"updated"`
}

type accountRecord struct {
	AccountInfo
	credentials     accountCredentials
	operator        accountOperator
	endpoint        string
	identityVersion int64
	refresh         accountRefreshState
}
type accountPrivate struct {
	Credentials     accountCredentials  `json:"credentials"`
	Operator        accountOperator     `json:"operator,omitempty"`
	Endpoint        string              `json:"endpoint,omitempty"`
	Refresh         accountRefreshState `json:"refresh,omitempty"`
	IdentityVersion int64               `json:"identity_version,omitempty"`
}

func (s *store) account(name string) (accountRecord, error) {
	var a accountRecord
	var data string
	err := s.db.QueryRow("SELECT name,kind,state,generation,expires,updated,data FROM accounts WHERE name=?", name).Scan(&a.Name, &a.Kind, &a.State, &a.Generation, &a.ExpiresAt, &a.Updated, &data)
	if err != nil {
		return a, err
	}
	var private accountPrivate
	if err = json.Unmarshal([]byte(data), &private); err != nil {
		return a, err
	}
	a.credentials, a.operator, a.endpoint = private.Credentials, private.Operator, private.Endpoint
	a.refresh = private.Refresh
	a.identityVersion = private.IdentityVersion
	applyAccountAuthInfo(&a.AccountInfo, private)
	return a, nil
}
func (s *store) accountInfos() ([]AccountInfo, error) {
	rows, err := s.db.Query("SELECT name,kind,state,generation,expires,updated,data FROM accounts ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountInfo{}
	for rows.Next() {
		var a AccountInfo
		var data string
		if err = rows.Scan(&a.Name, &a.Kind, &a.State, &a.Generation, &a.ExpiresAt, &a.Updated, &data); err != nil {
			return nil, err
		}
		var private accountPrivate
		if err = json.Unmarshal([]byte(data), &private); err != nil {
			return nil, err
		}
		applyAccountAuthInfo(&a, private)
		out = append(out, a)
	}
	return out, rows.Err()
}

// accountWrite uses FULL only for custody commits. The single SQLite connection
// prevents unrelated writes from seeing the temporary setting. Never hold it
// during an issuer call. Restore NORMAL for the existing generic request ledger.
func (s *store) accountWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	defer func() {
		reset, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(reset, "PRAGMA synchronous=NORMAL")
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		// database/sql marks a Tx done even when SQLite rejects COMMIT (for example
		// a deferred constraint). Explicitly end any remaining SQLite transaction
		// before returning this connection to the pool or restoring its PRAGMA.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = conn.ExecContext(cleanup, "ROLLBACK")
		cancel()
		return err
	}
	return nil
}
func encodeAccount(a accountRecord) ([]byte, error) {
	return json.Marshal(accountPrivate{Credentials: a.credentials, Operator: a.operator, Endpoint: a.endpoint, Refresh: a.refresh, IdentityVersion: a.identityVersion})
}
func (s *store) replaceAccount(ctx context.Context, a accountRecord, expected int64) error {
	data, err := encodeAccount(a)
	if err != nil {
		return err
	}
	return s.accountWrite(ctx, func(tx *sql.Tx) error {
		var result sql.Result
		var e error
		if expected == 0 {
			var n int
			if e = tx.QueryRow("SELECT count(*) FROM accounts").Scan(&n); e != nil {
				return e
			}
			if n >= 128 {
				return inputError("at most 128 accounts")
			}
			_, e = tx.Exec("INSERT INTO accounts(name,kind,state,generation,expires,updated,data) VALUES(?,?,?,?,?,?,?)", a.Name, a.Kind, a.State, a.Generation, a.ExpiresAt, a.Updated, string(data))
			return e
		}
		result, e = tx.Exec("UPDATE accounts SET kind=?,state=?,generation=?,expires=?,updated=?,data=? WHERE name=? AND generation=?", a.Kind, a.State, a.Generation, a.ExpiresAt, a.Updated, string(data), a.Name, expected)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("account changed")
		}
		return nil
	})
}
func (s *store) fenceAccount(ctx context.Context, name string, generation int64) error {
	return s.accountWrite(ctx, func(tx *sql.Tx) error {
		result, err := tx.Exec("UPDATE accounts SET state='refreshing',updated=? WHERE name=? AND generation=? AND state='ready'", time.Now().UnixMilli(), name, generation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("account not ready")
		}
		return nil
	})
}
func (s *store) interruptAccounts(ctx context.Context) error {
	return s.accountWrite(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE accounts SET state=CASE WHEN kind='copilot' THEN 'ready' ELSE 'needs_login' END WHERE state='refreshing'")
		return err
	})
}

func applyAccountAuthInfo(info *AccountInfo, private accountPrivate) {
	info.AuthMode = accountAuthMode(info.Kind, private.Credentials)
	if info.AuthMode == "token_only" && (private.Refresh.RejectedGeneration == info.Generation || info.ExpiresAt > 0 && info.ExpiresAt <= time.Now().Unix()) {
		// The rejection proof is sufficient even if a separate state-column write
		// failed: it is durable needs-login evidence, not a renewable-token fence.
		info.State = "needs_login"
	}
}
