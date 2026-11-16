package proxy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

func connectRoute(account string) Route {
	return Route{Name: account, LocalPrefix: "/" + account + "/", Upstream: "https://api.anthropic.com", Enabled: true, Account: account, CallerAuth: Auth{Header: "Authorization", Prefix: "Bearer "}, Usage: "auto", Billing: "plan", Redirects: "refuse", Allow: []string{"POST /v1/messages", "POST /v1/messages/count_tokens", "GET /v1/models", "GET /api/oauth/usage", "GET /api/oauth/profile"}}
}
func connectSpec(route Route) string {
	if len(route.SetHeaders) == 0 {
		route.SetHeaders = nil
	}
	route.CallerAuth.Header = http.CanonicalHeaderKey(route.CallerAuth.Header)
	// compile normalizes the order of semantically unordered allow rules.
	if validatePolicy(&route) != nil {
		return ""
	}
	data, _ := json.Marshal(route)
	return hashToken(string(data))
}
func expectedConnectRoute(account string) (activeRoute, error) {
	snap, err := compile(Config{Routes: []Route{connectRoute(account)}}, 0, nil)
	if err != nil {
		return activeRoute{}, connectProblem{409, "Account name conflicts with the reserved gateway namespace"}
	}
	return snap.routes[0], nil
}
func readConnectAccount(tx *sql.Tx, name string) (accountRecord, error) {
	var a accountRecord
	var data string
	err := tx.QueryRow("SELECT name,kind,state,generation,expires,updated,data FROM accounts WHERE name=?", name).Scan(&a.Name, &a.Kind, &a.State, &a.Generation, &a.ExpiresAt, &a.Updated, &data)
	if err != nil {
		return a, err
	}
	var private accountPrivate
	if err = json.Unmarshal([]byte(data), &private); err != nil {
		return a, err
	}
	a.credentials, a.operator, a.endpoint = private.Credentials, private.Operator, private.Endpoint
	a.refresh, a.identityVersion = private.Refresh, private.IdentityVersion
	applyAccountAuthInfo(&a.AccountInfo, private)
	return a, nil
}

type connectInspection struct {
	connectRequest
	Error string `json:"error,omitempty"`
}

func publicConnectMetadata(record connectReceipt) connectRequest {
	out := record.connectRequest
	if out.Result != nil {
		result := *out.Result
		result.Token = ""
		out.Result = &result
	}
	return out
}
func (s *Service) connectAdmin(w http.ResponseWriter, r *http.Request, path string, sess session) bool {
	if path != "connect/request" && path != "connect/approve" {
		return false
	}
	want := http.MethodGet
	if path == "connect/approve" {
		want = http.MethodPost
	}
	if r.Method != want {
		fail(w, 405, "Method not allowed")
		return true
	}
	id, code := r.URL.Query().Get("id"), ""
	if path == "connect/approve" {
		var in struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		}
		if !readJSON(w, r, &in) {
			return true
		}
		id, code = in.ID, in.Code
	}
	if !connectHex(id, 32) {
		fail(w, 400, "Invalid connect request id")
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var reply connectInspection
	err := s.connectTransaction(r.Context(), func(tx *sql.Tx) error {
		record, e := readConnect(tx, id)
		if e == sql.ErrNoRows {
			return connectProblem{404, "Connect request unavailable"}
		}
		if e != nil {
			return e
		}
		origin, _ := s.browserOrigin(r)
		if record.Origin != origin {
			return connectProblem{409, "Connect origin no longer matches"}
		}
		reply.connectRequest = publicConnectMetadata(record)
		if path == "connect/request" && record.Status != "pending" {
			return nil
		}
		if record.Status != "pending" {
			return connectProblem{409, "Connect request is not pending or has expired"}
		}
		proof, e := s.resolveConnect(tx, record)
		if e != nil {
			if path == "connect/request" {
				var problem connectProblem
				if !errors.As(e, &problem) {
					return e
				}
				reply.ResolvedAction = "collision"
				reply.Error = problem.message
				return nil
			}
			return e
		}
		reply.ResolvedAction = proof.Action
		reply.SourceAccount = connectPublicSource(record, proof)
		if path == "connect/request" {
			return nil
		}
		if !connectHex(code, 8) || record.Code != code {
			return connectProblem{409, "Pairing code does not match"}
		}
		if s.passkey != nil && !s.consumeRecentAuth(w, sess) {
			return errConnectResponseWritten
		}
		record.Status = "approved"
		record.ResolvedAction = proof.Action
		record.Proof = proof
		record.SourceAccount = connectPublicSource(record, proof)
		if e = writeConnect(tx, record); e != nil {
			return e
		}
		reply.connectRequest = publicConnectMetadata(record)
		return nil
	})
	if err == errConnectResponseWritten {
		return true
	}
	if err != nil {
		connectFailure(w, err)
	} else {
		jsonReply(w, 200, reply)
	}
	return true
}

var errConnectResponseWritten = errors.New("connect response already written")
