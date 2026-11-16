package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func connectCall(s *Service, method, action, secret string, body any) *httptest.ResponseRecorder {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "http://localhost/_proxy/api/connect/"+action, bytes.NewReader(data))
	req.Host = "localhost"
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}
func connectInput(i int, account, name, action string) connectStartInput {
	return connectStartInput{ID: fmt.Sprintf("%032x", i), Secret: fmt.Sprintf("%064x", i+1000), Account: account, ClientName: name, Action: action}
}
func startConnectTest(t *testing.T, s *Service, input connectStartInput) connectRequest {
	t.Helper()
	rr := connectCall(s, "POST", "start", "", input)
	if rr.Code != 200 {
		t.Fatalf("start: %d %s", rr.Code, rr.Body.String())
	}
	var reply connectRequest
	if json.Unmarshal(rr.Body.Bytes(), &reply) != nil {
		t.Fatal("invalid start response")
	}
	return reply
}
func approveConnectTest(t *testing.T, s *Service, input connectStartInput) connectRequest {
	t.Helper()
	pending := startConnectTest(t, s, input)
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": pending.Code}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatalf("approve: %d %s", rr.Code, rr.Body.String())
	}
	var result connectRequest
	json.Unmarshal(rr.Body.Bytes(), &result)
	return result
}
func commitConnectTest(t *testing.T, s *Service, input connectStartInput, token string) connectRequest {
	t.Helper()
	rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: token})
	if rr.Code != 200 {
		t.Fatalf("commit: %d %s", rr.Code, rr.Body.String())
	}
	var result connectRequest
	json.Unmarshal(rr.Body.Bytes(), &result)
	return result
}
func assertConnectCounts(t *testing.T, s *Service, accounts, routes, callers, grants int) {
	t.Helper()
	for table, want := range map[string]int{"accounts": accounts, "callers": callers, "grants": grants} {
		var n int
		if err := s.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s=%d want%d err%v", table, n, want, err)
		}
	}
	if len(s.snap.routes) != routes {
		t.Fatalf("routes=%d want%d", len(s.snap.routes), routes)
	}
}

func TestConnectAddApprovalCommitReceiptAndAck(t *testing.T) {
	s := testService(t)
	input := connectInput(1, "claude", "Laptop", "auto")
	pending := startConnectTest(t, s, input)
	if pending.Status != "pending" || pending.ResolvedAction != "" || pending.Result != nil || len(pending.Code) != 8 || pending.Origin != "http://localhost" || pending.Route != "claude" || pending.Prefix != "/claude/" {
		t.Fatal("pending leaked resolution or changed binding", pending)
	}
	assertConnectCounts(t, s, 0, 0, 0, 0)
	duplicate := startConnectTest(t, s, input)
	if duplicate.Code != pending.Code || duplicate.Expires != pending.Expires {
		t.Fatal("retry created new request")
	}
	var raw, hash string
	if err := s.store.db.QueryRow("SELECT secret_hash,data FROM connect_requests WHERE id=?", input.ID).Scan(&hash, &raw); err != nil {
		t.Fatal(err)
	}
	if hash != hashToken(input.Secret) || strings.Contains(raw, input.Secret) {
		t.Fatal("pair secret persisted in plaintext")
	}
	if rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: "synthetic-upstream"}); rr.Code != 409 {
		t.Fatal("unapproved commit accepted", rr.Code)
	}
	cookie, csrf := loginAdmin(t, s)
	inspect := adminRequest(s, "GET", "connect/request?id="+input.ID, nil, cookie, csrf)
	if inspect.Code != 200 || !strings.Contains(inspect.Body.String(), `"resolved_action":"add"`) {
		t.Fatal(inspect.Code, inspect.Body.String())
	}
	if rr := connectCall(s, "GET", "status?id="+input.ID, input.Secret, nil); strings.Contains(rr.Body.String(), "resolved_action") {
		t.Fatal("admin inspection leaked resolution to unapproved capability")
	}
	rr := adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": pending.Code}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	replay := adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": pending.Code}, cookie, csrf)
	if replay.Code != 409 {
		t.Fatal("approval replay", replay.Code)
	}
	result := commitConnectTest(t, s, input, "synthetic-upstream")
	if result.Status != "complete" || result.Result == nil || result.Result.Token == "" || result.Result.CallerID == "" || result.Result.GatewayURL != "http://localhost/claude/" {
		t.Fatal("incomplete result", result)
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
	account, err := s.store.account("claude")
	if err != nil || account.AuthMode != "token_only" || account.credentials.AccessToken != "synthetic-upstream" || account.ExpiresAt != 0 {
		t.Fatal("wrong trusted credential", account.AccountInfo, err)
	}
	route := s.snap.routes[0]
	if route.Upstream != "https://api.anthropic.com" || route.Account != "claude" || route.Redirects != "refuse" || route.Usage != "auto" || route.Billing != "plan" {
		t.Fatal("route policy changed")
	}
	if _, err = s.store.authorize(result.Result.Token, route); err != nil {
		t.Fatal("scoped caller missing exact grant", err)
	}
	var count int
	if err = s.store.db.QueryRow("SELECT count(*) FROM grants WHERE caller=?", result.Result.CallerID).Scan(&count); err != nil || count != 1 {
		t.Fatal("device received extra grants", count, err)
	}
	again := commitConnectTest(t, s, input, "different-upstream-must-not-replace")
	if again.Result.Token != result.Result.Token || again.Result.CallerID != result.Result.CallerID {
		t.Fatal("duplicate commit minted another credential")
	}
	account, _ = s.store.account("claude")
	if account.Generation != 1 || account.credentials.AccessToken != "synthetic-upstream" {
		t.Fatal("duplicate commit changed account")
	}
	if err = s.store.db.QueryRow("SELECT data FROM connect_requests WHERE id=?", input.ID).Scan(&raw); err != nil || strings.Contains(raw, "synthetic-upstream") {
		t.Fatal("upstream credential retained in receipt", err)
	}
	ack := connectCall(s, "POST", "ack", input.Secret, map[string]string{"id": input.ID})
	if ack.Code != 200 || strings.Contains(ack.Body.String(), result.Result.Token) {
		t.Fatal("ack did not erase token", ack.Code)
	}
	after := commitConnectTest(t, s, input, "")
	if after.Result.Token != "" || after.Result.CallerID != result.Result.CallerID {
		t.Fatal("ack reminted a proxy token")
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}

func TestConnectDeviceAndAccountOnlyReconnectPreserveOldCallers(t *testing.T) {
	s := testService(t)
	first := connectInput(11, "claude", "Existing laptop", "auto")
	approveConnectTest(t, s, first)
	old := commitConnectTest(t, s, first, "synthetic-original")
	authority := s.snap.routes[0].authority
	device := connectInput(12, "claude", "New tablet", "auto")
	approved := approveConnectTest(t, s, device)
	if approved.ResolvedAction != "device" {
		t.Fatal(approved)
	}
	if rr := connectCall(s, "POST", "commit", device.Secret, connectCommitInput{ID: device.ID, Token: "must-not-be-accepted"}); rr.Code != 400 {
		t.Fatal("device accepted upstream token", rr.Code)
	}
	added := commitConnectTest(t, s, device, "")
	if added.Result.Token == "" || added.Result.CallerID == old.Result.CallerID {
		t.Fatal("device did not get a new caller")
	}
	reconnect := connectInput(13, "claude", "", "reconnect")
	approved = approveConnectTest(t, s, reconnect)
	if approved.ResolvedAction != "reconnect" {
		t.Fatal(approved)
	}
	result := commitConnectTest(t, s, reconnect, "synthetic-replacement")
	if result.Result.Token != "" || result.Result.CallerID != "" || result.Result.Account != "claude" || result.Result.GatewayURL != "http://localhost/claude/" {
		t.Fatal("account-only reconnect minted client", result)
	}
	assertConnectCounts(t, s, 1, 1, 2, 2)
	a, err := s.store.account("claude")
	if err != nil || a.Generation != 2 || a.identityVersion != 1 || a.refresh.RejectedGeneration != 0 || a.credentials.AccessToken != "synthetic-replacement" {
		t.Fatal(a.AccountInfo, err)
	}
	if s.snap.routes[0].authority != authority {
		t.Fatal("reconnect changed authority")
	}
	for _, token := range []string{old.Result.Token, added.Result.Token} {
		if _, err = s.store.authorize(token, s.snap.routes[0]); err != nil {
			t.Fatal("reconnect removed old caller grant", err)
		}
	}
}

func TestConnectWrongCapabilitiesOriginExpiryAndBinding(t *testing.T) {
	s := testService(t)
	input := connectInput(21, "claude", "Laptop", "auto")
	startConnectTest(t, s, input)
	for _, action := range []string{"status?id=" + input.ID, "commit", "ack", "cancel"} {
		method := "POST"
		var body any = map[string]string{"id": input.ID}
		if strings.HasPrefix(action, "status") {
			method = "GET"
			body = nil
		}
		rr := connectCall(s, method, action, strings.Repeat("f", 64), body)
		if rr.Code != 401 {
			t.Fatalf("wrong capability %s %d", action, rr.Code)
		}
	}
	changed := input
	changed.ClientName = "Different"
	if rr := connectCall(s, "POST", "start", "", changed); rr.Code != 409 {
		t.Fatal("same id rebound", rr.Code)
	}
	changed = input
	changed.Secret = strings.Repeat("e", 64)
	if rr := connectCall(s, "POST", "start", "", changed); rr.Code != 409 {
		t.Fatal("same id secret replaced", rr.Code)
	}
	req := httptest.NewRequest("POST", "http://localhost/_proxy/api/connect/start", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.invalid")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatal("cross-origin public start", rr.Code)
	}
	if _, err := s.store.db.Exec("UPDATE connect_requests SET expires=? WHERE id=?", time.Now().Add(-time.Second).Unix(), input.ID); err != nil {
		t.Fatal(err)
	}
	rr = connectCall(s, "GET", "status?id="+input.ID, input.Secret, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"status":"expired"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if rr = connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: "synthetic"}); rr.Code != 410 {
		t.Fatal("expired request committed", rr.Code)
	}
	assertConnectCounts(t, s, 0, 0, 0, 0)
}
