package proxy

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type connectStartInput struct {
	ID         string `json:"id"`
	Secret     string `json:"secret"`
	Account    string `json:"account"`
	ClientName string `json:"client_name"`
	Action     string `json:"action"`
}
type connectCommitInput struct {
	ID        string `json:"id"`
	Token     string `json:"token,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

func connectHex(value string, n int) bool {
	if len(value) != n {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func connectFailure(w http.ResponseWriter, err error) {
	var problem connectProblem
	if errors.As(err, &problem) {
		fail(w, problem.status, problem.message)
		return
	}
	fail(w, 503, "Connect operation unavailable; recover with the same request id and capability")
}

// connectPublic is deliberately exact and runs only after the host's existing
// Origin/Host checks. Pair capabilities cannot authorize any browser admin API.
func (s *Service) connectPublic(w http.ResponseWriter, r *http.Request, origin string) bool {
	var action string
	switch r.URL.EscapedPath() {
	case "/_proxy/api/connect/start":
		action = "start"
	case "/_proxy/api/connect/status":
		action = "status"
	case "/_proxy/api/connect/commit":
		action = "commit"
	case "/_proxy/api/connect/ack":
		action = "ack"
	case "/_proxy/api/connect/cancel":
		action = "cancel"
	default:
		return false
	}
	want := http.MethodPost
	if action == "status" {
		want = http.MethodGet
	}
	if r.Method != want {
		fail(w, 405, "Method not allowed")
		return true
	}
	select {
	case s.slots <- struct{}{}:
	default:
		fail(w, 503, "Proxy concurrency limit")
		return true
	}
	defer func() { <-s.slots }()
	if action == "start" {
		s.connectStart(w, r, origin)
		return true
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || !connectHex(strings.TrimPrefix(values[0], "Bearer "), 64) {
		fail(w, 401, "Connect capability required")
		return true
	}
	secret := strings.TrimPrefix(values[0], "Bearer ")
	var input connectCommitInput
	if action == "status" {
		input.ID = r.URL.Query().Get("id")
	} else if action == "commit" {
		if !readJSON(w, r, &input) {
			return true
		}
	} else {
		var onlyID struct {
			ID string `json:"id"`
		}
		if !readJSON(w, r, &onlyID) {
			return true
		}
		input.ID = onlyID.ID
	}
	if !connectHex(input.ID, 32) {
		fail(w, 400, "Invalid connect request id")
		return true
	}
	if action == "commit" {
		s.connectCommit(w, r, origin, secret, input)
		return true
	}
	var reply connectRequest
	err := s.connectTransaction(r.Context(), func(tx *sql.Tx) error {
		record, e := authorizedConnect(tx, input.ID, secret, origin)
		if e != nil {
			return e
		}
		switch action {
		case "ack":
			if record.Status != "complete" {
				return connectProblem{409, "Only a completed request can be acknowledged"}
			}
			if record.Result != nil {
				record.Result.Token = ""
			}
			record.resultExpires = 0
			if e = writeConnect(tx, record); e != nil {
				return e
			}
		case "cancel":
			if record.Status == "pending" || record.Status == "approved" {
				record.Status = "canceled"
				if e = writeConnect(tx, record); e != nil {
					return e
				}
			}
		}
		reply = record.connectRequest
		return nil
	})
	if err != nil {
		connectFailure(w, err)
	} else {
		jsonReply(w, 200, reply)
	}
	return true
}
func (s *Service) connectStart(w http.ResponseWriter, r *http.Request, origin string) {
	var input connectStartInput
	if !readJSON(w, r, &input) {
		return
	}
	if input.Account == "" {
		input.Account = "claude"
	}
	if input.Action == "" {
		input.Action = "auto"
	}
	input.ClientName = strings.TrimSpace(input.ClientName)
	if !connectHex(input.ID, 32) || !connectHex(input.Secret, 64) || !slug.MatchString(input.Account) || !utf8.ValidString(input.ClientName) || boundedAttribution(input.ClientName) != input.ClientName || (input.Action != "auto" && input.Action != "reconnect") || input.Action == "auto" && input.ClientName == "" {
		fail(w, 400, "Invalid connect request specification")
		return
	}
	if err := validatePrefix(connectRoute(input.Account), nil); err != nil {
		fail(w, 400, "Account name conflicts with the reserved gateway namespace")
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if len(peer) > 128 {
		peer = "unknown"
	}
	now := time.Now()
	record := connectReceipt{connectRequest: connectRequest{ID: input.ID, Code: randomToken("")[:8], Origin: origin, Expires: now.Add(connectLifetime).Unix(), Status: "pending", Action: input.Action, Account: input.Account, ClientName: input.ClientName, Route: input.Account, Prefix: "/" + input.Account + "/"}, hash: hashToken(input.Secret), peer: peer, retained: now.Add(connectRetention).Unix()}
	reply := record.connectRequest
	err = s.connectTransaction(r.Context(), func(tx *sql.Tx) error {
		old, e := readConnect(tx, input.ID)
		if e == nil {
			if old.hash != record.hash || old.Origin != record.Origin || old.Action != record.Action || old.Account != record.Account || old.ClientName != record.ClientName {
				return connectProblem{409, "Connect request id already binds another specification"}
			}
			reply = old.connectRequest
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var total, pending, fromPeer int
		if e = tx.QueryRow("SELECT count(*),COALESCE(sum(status IN ('pending','approved')),0),COALESCE(sum(peer=? AND status IN ('pending','approved')),0) FROM connect_requests", peer).Scan(&total, &pending, &fromPeer); e != nil {
			return e
		}
		if total >= connectReceiptLimit && pending < connectPendingLimit && fromPeer < connectPeerLimit {
			removed, e := evictConnectTombstones(tx, total-connectReceiptLimit+1)
			if e != nil {
				return e
			}
			total -= int(removed)
		}
		if total >= connectReceiptLimit || pending >= connectPendingLimit || fromPeer >= connectPeerLimit {
			return connectProblem{429, "Connect capacity reached; wait for existing requests to expire"}
		}
		data, e := marshalConnect(record)
		if e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO connect_requests(id,secret_hash,peer,status,expires,retained_until,data) VALUES(?,?,?,?,?,?,?)", record.ID, record.hash, record.peer, record.Status, record.Expires, record.retained, data)
		return e
	})
	if err != nil {
		connectFailure(w, err)
	} else {
		jsonReply(w, 200, reply)
	}
}
