package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConnectFreshPasskeyOriginCSRFAndCapabilitySeparation(t *testing.T) {
	s, _ := passkeyFixture(t)
	b := newPasskeyBrowser(t, s)
	input := connectInput(31, "claude", "Laptop", "auto")
	rr := b.send("POST", "connect/start", input, 200)
	var pending connectRequest
	json.Unmarshal(rr.Body.Bytes(), &pending)
	b.send("GET", "connect/request?id="+input.ID, nil, 401)
	_, assert := bootstrapPasskey(t, b)
	approve := map[string]string{"id": input.ID, "code": pending.Code}
	b.send("POST", "connect/approve", approve, 403)
	body, _ := json.Marshal(approve)
	request := b.request("POST", "connect/approve", bytes.NewReader(body))
	request.Header.Set("Origin", "https://evil.invalid")
	b.execute(request, 403)
	request = b.request("POST", "connect/approve", bytes.NewReader(body))
	request.Header.Set("X-CSRF-Token", "wrong")
	b.execute(request, 403)
	assert("reauth", 1)
	b.send("POST", "connect/approve", map[string]string{"id": input.ID, "code": "ffffffff"}, 409)
	b.send("POST", "connect/approve", approve, 200)
	b.send("POST", "connect/approve", approve, 409)
	// A cookie alone cannot poll the private capability result.
	b.send("GET", "connect/status?id="+input.ID, nil, 401)
	request = b.request("GET", "connect/status?id="+input.ID, nil)
	request.Header.Set("Authorization", "Bearer "+input.Secret)
	b.execute(request, 200)
	assertConnectCounts(t, s, 0, 0, 0, 0)
}

func TestConnectPendingDoesNotRevealExistingAccountOrCollision(t *testing.T) {
	s := testService(t)
	for i, exists := range []bool{false, true} {
		if exists {
			r := connectRoute("claude")
			r.Account = ""
			configure(t, s, Config{Routes: []Route{r}}, nil)
		}
		input := connectInput(40+i, "claude", fmt.Sprintf("Laptop %d", i), "auto")
		pending := startConnectTest(t, s, input)
		if pending.Status != "pending" || pending.ResolvedAction != "" || pending.Result != nil {
			t.Fatal("pending disclosed account resolution", pending)
		}
		rr := connectCall(s, "GET", "status?id="+input.ID, input.Secret, nil)
		if rr.Code != 200 || strings.Contains(rr.Body.String(), "resolved_action") || strings.Contains(rr.Body.String(), "collision") || strings.Contains(rr.Body.String(), "error") {
			t.Fatal("pending status disclosed collision", rr.Body.String())
		}
	}
	cookie, csrf := loginAdmin(t, s)
	input := connectInput(41, "claude", "Laptop 1", "auto")
	rr := adminRequest(s, "GET", "connect/request?id="+input.ID, nil, cookie, csrf)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"resolved_action":"collision"`) {
		t.Fatal("admin did not see collision", rr.Code, rr.Body.String())
	}
}

func TestConnectUnmanagedBindingAndFileModeCannotBeAdopted(t *testing.T) {
	s := testService(t)
	input := connectInput(51, "claude", "Laptop", "auto")
	startConnectTest(t, s, input)
	s.opts.ConfigFile = "operator-file.yaml"
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "GET", "connect/request?id="+input.ID, nil, cookie, csrf)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "operator-file") {
		t.Fatal("file mode add was not clearly refused", rr.Code, rr.Body.String())
	}
	s.opts.ConfigFile = ""
	r := connectRoute("claude")
	r.Account = ""
	configure(t, s, Config{Routes: []Route{r}}, nil)
	rr = adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": startConnectTest(t, s, input).Code}, cookie, csrf)
	if rr.Code != 409 {
		t.Fatal("unmanaged exact-looking route adopted", rr.Code)
	}
	assertConnectCounts(t, s, 0, 1, 0, 0)
}

func TestConnectExpiryCancelResultTTLAndReceiptRecovery(t *testing.T) {
	s := testService(t)
	input := connectInput(61, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	if rr := connectCall(s, "POST", "cancel", input.Secret, map[string]string{"id": input.ID}); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"status":"canceled"`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID, Token: "synthetic"}); rr.Code != 409 {
		t.Fatal("canceled request committed", rr.Code)
	}
	second := connectInput(62, "claude", "Laptop", "auto")
	approveConnectTest(t, s, second)
	complete := commitConnectTest(t, s, second, "synthetic-token")
	if rr := connectCall(s, "POST", "cancel", second.Secret, map[string]string{"id": second.ID}); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"status":"complete"`) {
		t.Fatal("cancel changed committed effects", rr.Code)
	}
	if _, err := s.store.db.Exec("UPDATE connect_requests SET result_expires=? WHERE id=?", time.Now().Add(-time.Second).Unix(), second.ID); err != nil {
		t.Fatal(err)
	}
	rr := connectCall(s, "GET", "status?id="+second.ID, second.Secret, nil)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), complete.Result.Token) || !strings.Contains(rr.Body.String(), complete.Result.CallerID) {
		t.Fatal("expired result retained token or lost recovery metadata", rr.Code, rr.Body.String())
	}
	var raw string
	s.store.db.QueryRow("SELECT data FROM connect_requests WHERE id=?", second.ID).Scan(&raw)
	if strings.Contains(raw, complete.Result.Token) {
		t.Fatal("private result token not wiped")
	}
	retry := commitConnectTest(t, s, second, "")
	if retry.Result.Token != "" || retry.Result.CallerID != complete.Result.CallerID {
		t.Fatal("expired result reminted")
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}

func TestConnectPendingCapacityIsBoundedPerPeer(t *testing.T) {
	s := testService(t)
	for i := range connectPeerLimit {
		startConnectTest(t, s, connectInput(100+i, "claude", fmt.Sprint("Laptop", i), "auto"))
	}
	input := connectInput(200, "claude", "Extra", "auto")
	rr := connectCall(s, "POST", "start", "", input)
	if rr.Code != 429 {
		t.Fatal("peer capacity unbounded", rr.Code)
	}
	first := connectInput(100, "claude", "Laptop0", "auto")
	connectCall(s, "POST", "cancel", first.Secret, map[string]string{"id": first.ID})
	if rr = connectCall(s, "POST", "start", "", input); rr.Code != 200 {
		t.Fatal("cancellation did not release pending capacity", rr.Code)
	}
}

func TestConnectPublicPathsAreExactAndDoNotGrantAdmin(t *testing.T) {
	s := testService(t)
	input := connectInput(71, "claude", "Laptop", "auto")
	pending := startConnectTest(t, s, input)
	if rr := connectCall(s, "POST", "approve", input.Secret, map[string]string{"id": input.ID, "code": pending.Code}); rr.Code != 401 {
		t.Fatal("capability approved itself", rr.Code)
	}
	for _, path := range []string{"start/", "%73tart", "request?id=" + input.ID} {
		req := httptest.NewRequest("POST", "http://localhost/_proxy/api/connect/"+path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "http://localhost")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+input.Secret)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 401 {
			t.Fatal("nonexact path exposed public/admin capability", path, rr.Code)
		}
	}
}

func TestConnectManagedDeviceRequiresReadyNonRejectedAccount(t *testing.T) {
	for _, state := range []string{"unknown_expiry", "expired", "durable_rejection", "memory_rejection"} {
		t.Run(state, func(t *testing.T) {
			s := testService(t)
			first := connectInput(401, "claude", "Original", "auto")
			approveConnectTest(t, s, first)
			commitConnectTest(t, s, first, "synthetic-original")
			switch state {
			case "expired":
				expiry := time.Now().Add(-time.Second).Unix()
				if _, err := s.store.db.Exec("UPDATE accounts SET expires=?,data=json_set(data,'$.credentials.expires_at',?) WHERE name='claude'", expiry, expiry); err != nil {
					t.Fatal(err)
				}
			case "durable_rejection":
				if _, err := s.store.db.Exec("UPDATE accounts SET data=json_set(data,'$.refresh.rejected_generation',generation) WHERE name='claude'"); err != nil {
					t.Fatal(err)
				}
			case "memory_rejection":
				s.accountRejected.Store("claude", int64(1))
			}
			next := connectInput(402, "claude", "New device", "auto")
			pending := startConnectTest(t, s, next)
			if pending.ResolvedAction != "" {
				t.Fatal("start leaked readiness")
			}
			cookie, csrf := loginAdmin(t, s)
			rr := adminRequest(s, "GET", "connect/request?id="+next.ID, nil, cookie, csrf)
			want := `"resolved_action":"collision"`
			if state == "unknown_expiry" {
				want = `"resolved_action":"device"`
			}
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), want) {
				t.Fatal("readiness not enforced", state, rr.Code, rr.Body.String())
			}
			if state != "unknown_expiry" {
				reconnect := connectInput(403, "claude", "", "reconnect")
				approved := approveConnectTest(t, s, reconnect)
				if approved.ResolvedAction != "reconnect" {
					t.Fatal("explicit reconnect unavailable", approved)
				}
			}
			assertConnectCounts(t, s, 1, 1, 1, 1)
		})
	}
}

func TestConnectManagedDeviceAndReconnectWorkWithoutEditingOperatorFile(t *testing.T) {
	s := testService(t)
	first := connectInput(501, "claude", "Original", "auto")
	approveConnectTest(t, s, first)
	commitConnectTest(t, s, first, "synthetic-original")
	revision := s.snap.revision
	s.opts.ConfigFile = "operator-managed.yaml"
	device := connectInput(502, "claude", "Second", "auto")
	approveConnectTest(t, s, device)
	commitConnectTest(t, s, device, "")
	reconnect := connectInput(503, "claude", "", "reconnect")
	approveConnectTest(t, s, reconnect)
	commitConnectTest(t, s, reconnect, "synthetic-replacement")
	if s.snap.revision != revision {
		t.Fatal("file-managed route was edited")
	}
	assertConnectCounts(t, s, 1, 1, 2, 2)
}

func TestConnectCapabilityCannotMoveOriginOrOverrideFrozenFields(t *testing.T) {
	s := testService(t)
	input := connectInput(601, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	request := httptest.NewRequest("GET", "http://localhost:9999/_proxy/api/connect/status?id="+input.ID, nil)
	request.Host = "localhost:9999"
	request.Header.Set("Authorization", "Bearer "+input.Secret)
	request.Header.Set("Origin", "http://localhost:9999")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, request)
	if rr.Code != 401 {
		t.Fatal("capability moved origin", rr.Code)
	}
	rr = connectCall(s, "POST", "commit", input.Secret, map[string]any{"id": input.ID, "token": "synthetic", "account": "other", "upstream": "https://evil.invalid"})
	if rr.Code != 400 {
		t.Fatal("commit changed frozen scope", rr.Code)
	}
	assertConnectCounts(t, s, 0, 0, 0, 0)
}

func TestConnectPendingGlobalAndReceiptCapacityAreBounded(t *testing.T) {
	s := testService(t)
	for i := range connectPendingLimit {
		input := connectInput(800+i, "claude", fmt.Sprint("Device ", i), "auto")
		data, _ := json.Marshal(input)
		request := httptest.NewRequest("POST", "http://localhost/_proxy/api/connect/start", bytes.NewReader(data))
		request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", 1+i/connectPeerLimit)
		request.Header.Set("Origin", "http://localhost")
		request.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, request)
		if rr.Code != 200 {
			t.Fatal("premature global limit", i, rr.Code, rr.Body.String())
		}
	}
	input := connectInput(900, "claude", "Over limit", "auto")
	rr := connectCall(s, "POST", "start", "", input)
	if rr.Code != 429 {
		t.Fatal("global pending capacity unbounded", rr.Code)
	}
	if _, err := s.store.db.Exec("UPDATE connect_requests SET status='canceled'"); err != nil {
		t.Fatal(err)
	}
	// Fill retained nonprivate receipts directly to test the total bound without
	// waiting through five-minute pending lifetimes or weakening their TTL.
	for i := connectPendingLimit; i < connectReceiptLimit; i++ {
		id := fmt.Sprintf("%032x", 10000+i)
		if _, err := s.store.db.Exec("INSERT INTO connect_requests SELECT ?,secret_hash,peer,'canceled',expires,retained_until,0,data FROM connect_requests LIMIT 1", id); err != nil {
			t.Fatal(err)
		}
	}
	rr = connectCall(s, "POST", "start", "", input)
	if rr.Code != 429 {
		t.Fatal("receipt retention capacity unbounded", rr.Code)
	}
}

func TestConnectCompletedPairExpiryLeavesOnlyNonsecretRecoveryReceipt(t *testing.T) {
	s := testService(t)
	input := connectInput(1001, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	complete := commitConnectTest(t, s, input, "synthetic")
	// Pair expiry is an upper bound even when the commit occurred just before it.
	var expires, resultExpires int64
	if err := s.store.db.QueryRow("SELECT expires,result_expires FROM connect_requests WHERE id=?", input.ID).Scan(&expires, &resultExpires); err != nil || resultExpires > expires {
		t.Fatal("result escaped pairing expiry", expires, resultExpires, err)
	}
	if _, err := s.store.db.Exec("UPDATE connect_requests SET result_expires=? WHERE id=?", time.Now().Add(-time.Second).Unix(), input.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.retainConnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rr := connectCall(s, "GET", "status?id="+input.ID, input.Secret, nil)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), complete.Result.Token) || !strings.Contains(rr.Body.String(), `"status":"complete"`) {
		t.Fatal("completed recovery exposed private result", rr.Code, rr.Body.String())
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}

func TestConnectPressureEvictsOnlyExpiredUnapprovedTombstones(t *testing.T) {
	s := testService(t)
	completeInput := connectInput(2001, "claude", "Complete", "auto")
	approveConnectTest(t, s, completeInput)
	complete := commitConnectTest(t, s, completeInput, "synthetic")
	approvedInput := connectInput(2002, "claude", "Approved", "auto")
	approveConnectTest(t, s, approvedInput)
	expiredApproval := connectInput(2003, "claude", "Expired approval", "auto")
	approveConnectTest(t, s, expiredApproval)
	activeInput := connectInput(2004, "claude", "Active", "auto")
	startConnectTest(t, s, activeInput)
	tombstone := connectInput(2005, "claude", "Unapproved tombstone", "auto")
	old := startConnectTest(t, s, tombstone)
	if _, err := s.store.db.Exec("UPDATE connect_requests SET expires=? WHERE id IN (?,?)", time.Now().Add(-time.Minute).Unix(), tombstone.ID, expiredApproval.ID); err != nil {
		t.Fatal(err)
	}
	// Fill the store with expired never-approved receipts using the unapproved
	// row's shape, keeping completed and approved receipts as protected sentinels.
	for i := 5; i < connectReceiptLimit; i++ {
		id := fmt.Sprintf("%032x", 30000+i)
		if _, err := s.store.db.Exec("INSERT INTO connect_requests SELECT ?,secret_hash,peer,'expired',expires,retained_until,0,data FROM connect_requests WHERE id=?", id, tombstone.ID); err != nil {
			t.Fatal(err)
		}
	}
	fresh := connectInput(2006, "claude", "Fresh device", "auto")
	startConnectTest(t, s, fresh)
	for _, id := range []string{completeInput.ID, approvedInput.ID, expiredApproval.ID, activeInput.ID} {
		var count int
		if err := s.store.db.QueryRow("SELECT count(*) FROM connect_requests WHERE id=?", id).Scan(&count); err != nil || count != 1 {
			t.Fatal("protected receipt evicted", id, count, err)
		}
	}
	rr := connectCall(s, "GET", "status?id="+completeInput.ID, completeInput.Secret, nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), complete.Result.Token) {
		t.Fatal("completed private recovery receipt lost", rr.Code)
	}
	var count int
	s.store.db.QueryRow("SELECT count(*) FROM connect_requests WHERE id=?", tombstone.ID).Scan(&count)
	if count != 0 {
		t.Fatal("oldest authority-free tombstone was not evicted")
	}
	// Reusing an evicted authority-free id is not a replay: it is pending with a
	// new matching code, and no approval or account/caller side effects.
	again := startConnectTest(t, s, tombstone)
	if again.Status != "pending" || again.ResolvedAction != "" || again.Code == old.Code {
		t.Fatal("evicted tombstone replayed old approval", again)
	}
	assertConnectCounts(t, s, 1, 1, 1, 1)
}
