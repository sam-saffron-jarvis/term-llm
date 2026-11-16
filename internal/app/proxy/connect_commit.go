package proxy

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

func writeConnectAccount(tx *sql.Tx, a accountRecord, expected int64) error {
	data, err := encodeAccount(a)
	if err != nil {
		return err
	}
	if expected == 0 {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM accounts").Scan(&n); err != nil {
			return err
		}
		if n >= 128 {
			return connectProblem{409, "Account capacity reached"}
		}
		_, err = tx.Exec("INSERT INTO accounts(name,kind,state,generation,expires,updated,data) VALUES(?,?,?,?,?,?,?)", a.Name, a.Kind, a.State, a.Generation, a.ExpiresAt, a.Updated, string(data))
		return err
	}
	result, err := tx.Exec("UPDATE accounts SET kind=?,state=?,generation=?,expires=?,updated=?,data=? WHERE name=? AND generation=?", a.Kind, a.State, a.Generation, a.ExpiresAt, a.Updated, string(data), a.Name, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return connectConflict()
	}
	return nil
}
func createConnectCaller(tx *sql.Tx, name string, route activeRoute, now int64) (string, string, error) {
	if name == "" {
		return "", "", nil
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM callers").Scan(&n); err != nil {
		return "", "", err
	}
	if n >= 1024 {
		return "", "", connectProblem{409, "Device capacity reached"}
	}
	id, token := randomToken("c_")[:26], randomToken("sk-tlp-")
	if _, err := tx.Exec("INSERT INTO callers(id,name,hash,created) VALUES(?,?,?,?)", id, name, hashToken(token), now); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec("INSERT INTO grants(caller,route,authority) VALUES(?,?,?)", id, route.Name, route.authority); err != nil {
		return "", "", err
	}
	return id, token, nil
}
func (s *Service) commitConnectTx(tx *sql.Tx, record *connectReceipt, input connectCommitInput, next *snapshot, route activeRoute) (*accountRecord, error) {
	proof := record.Proof // verified against live authority in this same transaction
	var err error
	var changed *accountRecord
	if proof.Action == "device" {
		if input.Token != "" || input.ExpiresAt != 0 {
			return nil, connectProblem{400, "Device connection must not supply an upstream token"}
		}
	} else {
		credentials := accountCredentials{Mode: "token_only", AccessToken: input.Token, ExpiresAt: input.ExpiresAt}
		if err = validateAccountCredentials("claude-code", credentials); err != nil {
			return nil, connectProblem{400, "A single valid Claude access token and optional Unix-second expiry are required"}
		}
		if input.ExpiresAt != 0 && input.ExpiresAt <= time.Now().Unix() {
			return nil, connectProblem{400, "The supplied token expiry is already past"}
		}
		account := accountRecord{AccountInfo: AccountInfo{Name: proof.SourceAccount, Kind: "claude-code", AuthMode: "token_only", State: "ready", Generation: proof.Generation + 1, ExpiresAt: input.ExpiresAt, Updated: time.Now().UnixMilli()}, credentials: credentials}
		if proof.Action == "reconnect" {
			old, e := readConnectAccount(tx, proof.SourceAccount)
			if e != nil {
				return nil, e
			}
			account.identityVersion = old.identityVersion + 1
			account.operator = old.operator
			account.credentials.AccountID = old.credentials.AccountID
		}
		if err = writeConnectAccount(tx, account, proof.Generation); err != nil {
			return nil, err
		}
		changed = &account
	}
	if proof.AddRoute {
		if next == nil {
			return nil, connectConflict()
		}
		data, e := json.Marshal(next.config)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec("UPDATE configuration SET revision=?,data=? WHERE id=1", next.revision, string(data)); err != nil {
			return nil, err
		}
	}
	if proof.Adopt {
		if _, err = tx.Exec("INSERT INTO connect_managed(account,route,prefix,authority,spec,request_id) VALUES(?,?,?,?,?,?)", record.Account, record.Route, record.Prefix, proof.Authority, proof.Spec, record.ID); err != nil {
			return nil, err
		}
	}
	callerID, token, err := createConnectCaller(tx, record.ClientName, route, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	record.Status = "complete"
	record.Result = &connectResult{Token: token, CallerID: callerID, GatewayURL: record.Origin + record.Prefix, Account: record.Account, Route: record.Route}
	if token != "" {
		record.resultExpires = time.Now().Add(connectResultLifetime).Unix()
		if record.resultExpires > record.Expires {
			record.resultExpires = record.Expires
		}
	}
	if err = writeConnect(tx, *record); err != nil {
		return nil, err
	}
	return changed, nil
}
func (s *Service) connectCommit(w http.ResponseWriter, r *http.Request, origin, secret string, input connectCommitInput) {
	// Read the immutable account binding first, then take the same per-account
	// gate/global-lock order as trusted credential replacement. No network occurs.
	var binding connectReceipt
	err := s.connectTransaction(r.Context(), func(tx *sql.Tx) error {
		var e error
		binding, e = authorizedConnect(tx, input.ID, secret, origin)
		return e
	})
	if err != nil {
		connectFailure(w, err)
		return
	}
	if binding.Status != "approved" && binding.Status != "complete" {
		if binding.Status == "expired" {
			connectFailure(w, connectProblem{410, "Connect request expired; start a new request explicitly"})
		} else {
			connectFailure(w, connectProblem{409, "Connect request is not approved"})
		}
		return
	}
	targetAccount := binding.Proof.SourceAccount
	if targetAccount == "" {
		targetAccount = binding.Account
	}
	unlock, err := s.accountLock(r.Context(), targetAccount)
	if err != nil {
		connectFailure(w, err)
		return
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var reply connectRequest
	var next *snapshot
	var changed *accountRecord
	err = s.connectTransaction(r.Context(), func(tx *sql.Tx) error {
		record, e := authorizedConnect(tx, input.ID, secret, origin)
		if e != nil {
			return e
		}
		if record.Status == "complete" {
			if record.ResolvedAction == "device" && (input.Token != "" || input.ExpiresAt != 0) {
				return connectProblem{400, "Device connection must not supply an upstream token"}
			}
			reply = record.connectRequest
			return nil
		}
		if record.Status == "expired" {
			return connectProblem{410, "Connect request expired; start a new request explicitly"}
		}
		if record.Status != "approved" {
			return connectProblem{409, "Connect request is not approved or has expired"}
		}
		proof, e := s.resolveConnect(tx, record)
		if e != nil {
			return e
		}
		if !sameConnectProof(proof, record.Proof) {
			return connectConflict()
		}

		// Snapshot preparation must not query the same single SQLite connection.
		// The configuration money mirror is already immutable under s.mu here.
		route, e := s.connectApprovedRoute(record)
		if e != nil {
			return e
		}
		if record.Proof.AddRoute {
			config := s.snap.config
			config.Routes = append(append([]Route(nil), config.Routes...), route.Route)
			next, e = compile(config, s.snap.revision+1, s.snap.secrets)
			if e != nil {
				return e
			}
		}
		changed, e = s.commitConnectTx(tx, &record, input, next, route)
		if e != nil {
			return e
		}
		reply = record.connectRequest
		return nil
	})
	if err != nil {
		connectFailure(w, err)
		return
	}
	if next != nil {
		s.snap = next
	}
	if changed != nil {
		s.rememberAccount(changed.Name, changed.credentials)
		s.accountRejected.Delete(changed.Name)
		s.cancelAccountSockets(changed.Name)
	}
	jsonReply(w, 200, reply)
}
