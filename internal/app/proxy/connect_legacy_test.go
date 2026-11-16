package proxy

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func installLegacyConnect(t *testing.T, s *Service, name, routeName string) accountRecord {
	t.Helper()
	expiry := time.Now().Add(time.Hour).Unix()
	a := accountRecord{AccountInfo: AccountInfo{Name: name, Kind: "claude-code", State: "ready", Generation: 5, ExpiresAt: expiry, Updated: time.Now().UnixMilli()}, credentials: accountCredentials{AccessToken: "synthetic-legacy-" + name, RefreshToken: "synthetic-refresh-" + name, AccountID: "trusted-principal-" + name, ExpiresAt: expiry, Scopes: []string{"user:inference"}}, operator: accountOperator{Issuer: claudeTokenURL, ClientID: claudeClientID}, identityVersion: 2}
	if err := s.store.replaceAccount(context.Background(), a, 0); err != nil {
		t.Fatal(err)
	}
	route := connectRoute(routeName)
	route.Account = name
	route.Allow = []string{"POST /v1/messages"}
	config := s.snap.config
	config.Routes = append(append([]Route(nil), config.Routes...), route)
	configure(t, s, config, nil)
	s.rememberAccount(name, a.credentials)
	return a
}

func TestConnectLegacyWorkingAccountCreatesOnlyNarrowDeviceAlias(t *testing.T) {
	s := testService(t)
	legacy := installLegacyConnect(t, s, "family-claude", "claude-code")
	oldToken := caller(t, s, "claude-code")
	var before string
	s.store.db.QueryRow("SELECT data FROM accounts WHERE name=?", legacy.Name).Scan(&before)
	sourceSpec, sourceAuthority := connectSpec(s.snap.routes[0].Route), s.snap.routes[0].authority
	var dials atomic.Int32
	s.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("connect must not probe any vendor")
	}
	input := connectInput(1101, "claude", "New laptop", "auto")
	pending := startConnectTest(t, s, input)
	if pending.SourceAccount != "" || pending.ResolvedAction != "" {
		t.Fatal("public pending leaked legacy discovery", pending)
	}
	cookie, csrf := loginAdmin(t, s)
	preview := adminRequest(s, "GET", "connect/request?id="+input.ID, nil, cookie, csrf)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"source_account":"family-claude"`) || !strings.Contains(preview.Body.String(), `"resolved_action":"device"`) {
		t.Fatal("authenticated preview did not identify source", preview.Code, preview.Body.String())
	}
	status := connectCall(s, "GET", "status?id="+input.ID, input.Secret, nil)
	if strings.Contains(status.Body.String(), "family-claude") || strings.Contains(status.Body.String(), "source_account") {
		t.Fatal("discovery leaked before approval")
	}
	approved := approveConnectTest(t, s, input)
	if approved.ResolvedAction != "device" || approved.SourceAccount != "family-claude" {
		t.Fatal("legacy account required new login", approved)
	}
	complete := commitConnectTest(t, s, input, "")
	if complete.SourceAccount != "family-claude" || complete.Result.GatewayURL != "http://localhost/claude/" || complete.Result.Account != "claude" {
		t.Fatal("alias contract changed", complete)
	}
	assertConnectCounts(t, s, 1, 2, 2, 2)
	var after string
	s.store.db.QueryRow("SELECT data FROM accounts WHERE name=?", legacy.Name).Scan(&after)
	if before != after {
		t.Fatal("device pairing replaced or normalized legacy credentials")
	}
	alias := s.connectNamedRoute("claude")
	source := s.connectNamedRoute("claude-code")
	if alias == nil || source == nil || alias.Account != "family-claude" || !reflect.DeepEqual(alias.Allow, source.Allow) || connectSpec(source.Route) != sourceSpec || source.authority != sourceAuthority {
		t.Fatal("alias widened/changed existing route")
	}
	if _, err := s.store.authorize(oldToken, *source); err != nil {
		t.Fatal("old grant changed", err)
	}
	if _, err := s.store.authorize(complete.Result.Token, *alias); err != nil {
		t.Fatal("new alias grant unavailable", err)
	}
	if _, err := s.store.authorize(complete.Result.Token, *source); err == nil {
		t.Fatal("new device gained source route grant")
	}
	if dials.Load() != 0 {
		t.Fatal("device pairing contacted issuer/vendor", dials.Load())
	}
}

func TestConnectLegacyExactRouteAdoptionAndReconnectTargetActualAccount(t *testing.T) {
	s := testService(t)
	legacy := installLegacyConnect(t, s, "family-claude", "claude-code")
	oldToken := caller(t, s, "claude-code")
	revision := s.snap.revision
	authority := s.snap.routes[0].authority
	input := connectInput(1201, "claude-code", "New laptop", "auto")
	approved := approveConnectTest(t, s, input)
	if approved.ResolvedAction != "device" || approved.SourceAccount != "family-claude" {
		t.Fatal(approved)
	}
	device := commitConnectTest(t, s, input, "")
	assertConnectCounts(t, s, 1, 1, 2, 2)
	if s.snap.revision != revision || s.snap.routes[0].authority != authority {
		t.Fatal("exact route reuse changed configuration")
	}
	reconnect := connectInput(1202, "claude-code", "", "reconnect")
	approved = approveConnectTest(t, s, reconnect)
	if approved.SourceAccount != "family-claude" {
		t.Fatal("managed alias forgot actual account", approved)
	}
	result := commitConnectTest(t, s, reconnect, "synthetic-explicit-reconnect")
	if result.Result.Token != "" || result.Result.CallerID != "" {
		t.Fatal("account-only reconnect created a new device")
	}
	assertConnectCounts(t, s, 1, 1, 2, 2)
	actual, err := s.store.account("family-claude")
	if err != nil || actual.Generation != 6 || actual.identityVersion != 3 || actual.credentials.Mode != "token_only" || actual.credentials.AccessToken != "synthetic-explicit-reconnect" || actual.credentials.AccountID != legacy.credentials.AccountID || !reflect.DeepEqual(actual.operator, legacy.operator) {
		t.Fatal("reconnect changed identity/operator fences or wrong account", actual.AccountInfo, err)
	}
	if _, err = s.store.authorize(oldToken, s.snap.routes[0]); err != nil {
		t.Fatal("old caller lost authority", err)
	}
	if _, err = s.store.authorize(device.Result.Token, s.snap.routes[0]); err != nil {
		t.Fatal("new caller lost authority", err)
	}
}

func TestConnectLegacyAmbiguityAndUnsafeSourcesRequireExplicitChoice(t *testing.T) {
	t.Run("ambiguous", func(t *testing.T) {
		s := testService(t)
		installLegacyConnect(t, s, "family-a", "claude-a")
		installLegacyConnect(t, s, "family-b", "claude-b")
		input := connectInput(1301, "claude", "Laptop", "auto")
		pending := startConnectTest(t, s, input)
		cookie, csrf := loginAdmin(t, s)
		preview := adminRequest(s, "GET", "connect/request?id="+input.ID, nil, cookie, csrf)
		if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"resolved_action":"collision"`) || !strings.Contains(preview.Body.String(), "choose an existing connection name") {
			t.Fatal("ambiguous source silently selected", preview.Code, preview.Body.String())
		}
		if rr := adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": pending.Code}, cookie, csrf); rr.Code != 409 {
			t.Fatal("ambiguous approval accepted", rr.Code)
		}
		assertConnectCounts(t, s, 2, 2, 0, 0)
	})
	for _, unsafe := range []string{"wide-path", "custom-origin", "wrong-auth", "usage-off"} {
		t.Run(unsafe, func(t *testing.T) {
			s := testService(t)
			installLegacyConnect(t, s, "family-claude", "claude-code")
			route := s.snap.config.Routes[0]
			switch unsafe {
			case "wide-path":
				route.Allow = []string{"POST /"}
			case "custom-origin":
				route.Upstream = "https://other.invalid"
			case "wrong-auth":
				route.CallerAuth = Auth{Header: "X-Caller-Key", Prefix: "Local "}
			case "usage-off":
				route.Usage = "off"
			}
			configure(t, s, Config{Routes: []Route{route}}, nil)
			input := connectInput(1302, "claude", "Laptop", "auto")
			pending := startConnectTest(t, s, input)
			cookie, csrf := loginAdmin(t, s)
			rr := adminRequest(s, "POST", "connect/approve", map[string]string{"id": input.ID, "code": pending.Code}, cookie, csrf)
			if rr.Code != 409 {
				t.Fatal("unsafe source adopted or new login silently requested", unsafe, rr.Code, rr.Body.String())
			}
			assertConnectCounts(t, s, 1, 1, 0, 0)
		})
	}
}

func TestConnectLegacySourceProofDriftDoesNotCreateAlias(t *testing.T) {
	s := testService(t)
	installLegacyConnect(t, s, "family-claude", "claude-code")
	input := connectInput(1401, "claude", "Laptop", "auto")
	approveConnectTest(t, s, input)
	source := s.snap.config.Routes[0]
	source.RequestIDHeader = "X-New-Request-ID"
	configure(t, s, Config{Routes: []Route{source}}, nil)
	rr := connectCall(s, "POST", "commit", input.Secret, connectCommitInput{ID: input.ID})
	if rr.Code != 409 {
		t.Fatal("source changed after approval", rr.Code, rr.Body.String())
	}
	assertConnectCounts(t, s, 1, 1, 0, 0)
}

func TestConnectNamedExistingAccountWithoutRouteDoesNotNeedToken(t *testing.T) {
	s := testService(t)
	a := accountRecord{AccountInfo: AccountInfo{Name: "claude", Kind: "claude-code", State: "ready", Generation: 3, Updated: time.Now().UnixMilli()}, credentials: accountCredentials{Mode: "token_only", AccessToken: "synthetic-existing"}}
	if err := s.store.replaceAccount(context.Background(), a, 0); err != nil {
		t.Fatal(err)
	}
	input := connectInput(1501, "claude", "Laptop", "auto")
	approved := approveConnectTest(t, s, input)
	if approved.ResolvedAction != "device" {
		t.Fatal("existing named credential forced enrollment", approved)
	}
	commitConnectTest(t, s, input, "")
	assertConnectCounts(t, s, 1, 1, 1, 1)
	a, err := s.store.account("claude")
	if err != nil || a.Generation != 3 || a.credentials.AccessToken != "synthetic-existing" {
		t.Fatal("named existing account replaced", a.AccountInfo, err)
	}
}
