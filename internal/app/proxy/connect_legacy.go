package proxy

import (
	"database/sql"
	"net/http"
	"time"
)

// Only the existing fixed Anthropic HTTP protocol is eligible. A legacy source
// may be narrower than the profile; an alias copies that exact allowlist, never
// turns a custom/wider route into the full profile by quietly changing scope.
func canonicalConnectRoute(route activeRoute) bool {
	if !route.Enabled || route.Account == "" || route.origin != "https://api.anthropic.com:443" || route.target == nil || route.target.EscapedPath() != "" && route.target.EscapedPath() != "/" || route.AllowHTTP || route.WebSocket || len(route.SetHeaders) != 0 {
		return false
	}
	if http.CanonicalHeaderKey(route.CallerAuth.Header) != "Authorization" || route.CallerAuth.Prefix != "Bearer " || route.Usage != "auto" || route.Billing != "plan" || route.Redirects != "refuse" || len(route.Allow) == 0 {
		return false
	}
	fixed := connectRoute(route.Name).Allow
	for _, rule := range route.Allow {
		found := false
		for _, allowed := range fixed {
			if rule == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func validConnectAccount(a accountRecord) bool {
	if a.Kind != "claude-code" || !accountOriginAllowed(a, "https://api.anthropic.com:443") {
		return false
	}
	c := a.credentials
	if a.State == "needs_login" && c.AccessToken == "" && c.RefreshToken == "" {
		return (c.Mode == "" || c.Mode == "token_only") && c.GitHubToken == "" && c.AccessKeyID == "" && c.SecretAccessKey == "" && c.SessionToken == ""
	}
	return validateAccountCredentials(a.Kind, c) == nil
}
func (s *Service) readyConnectAccount(a accountRecord) bool {
	return validConnectAccount(a) && a.State == "ready" && (a.ExpiresAt == 0 && tokenOnlyAccount(a) || a.ExpiresAt > time.Now().Unix()) && !s.accountGenerationRejected(a)
}
func (s *Service) connectNamedRoute(name string) *activeRoute {
	for i := range s.snap.routes {
		if s.snap.routes[i].Name == name {
			return &s.snap.routes[i]
		}
	}
	return nil
}
func connectPublicSource(record connectReceipt, proof connectProof) string {
	if proof.SourceAccount != record.Account {
		return proof.SourceAccount
	}
	return ""
}
func connectAlias(record connectReceipt, source *activeRoute, account string) (activeRoute, error) {
	route := connectRoute(record.Account)
	if source != nil {
		route = source.Route
		route.Name = record.Route
		route.LocalPrefix = record.Prefix
		route.Upstream = "https://api.anthropic.com"
	}
	route.Account = account
	snap, err := compile(Config{Routes: []Route{route}}, 0, nil)
	if err != nil {
		return activeRoute{}, err
	}
	return snap.routes[0], nil
}
func connectProofFor(record connectReceipt, account accountRecord, source *activeRoute, target activeRoute, revision int64, addRoute, adopt bool) connectProof {
	proof := connectProof{Action: "device", SourceAccount: account.Name, Generation: account.Generation, Authority: target.authority, Spec: connectSpec(target.Route), Revision: revision, AddRoute: addRoute, Adopt: adopt}
	if source != nil {
		proof.SourceRoute = source.Name
		proof.SourceAuthority = source.authority
		proof.SourceSpec = connectSpec(source.Route)
	}
	return proof
}
func (s *Service) resolveConnect(tx *sql.Tx, record connectReceipt) (connectProof, error) {
	var revision int64
	if err := tx.QueryRow("SELECT revision FROM configuration WHERE id=1").Scan(&revision); err != nil {
		return connectProof{}, err
	}
	if revision != s.snap.revision {
		return connectProof{}, connectConflict()
	}
	if record.ClientName != "" {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM callers WHERE name=?", record.ClientName).Scan(&n); err != nil {
			return connectProof{}, err
		}
		if n != 0 {
			return connectProof{}, connectProblem{409, "Client name already exists; choose a distinct device name"}
		}
	}
	target := s.connectNamedRoute(record.Route)
	var boundRoute, prefix, authority, spec string
	managedErr := tx.QueryRow("SELECT route,prefix,authority,spec FROM connect_managed WHERE account=?", record.Account).Scan(&boundRoute, &prefix, &authority, &spec)
	if managedErr != nil && managedErr != sql.ErrNoRows {
		return connectProof{}, managedErr
	}
	if managedErr == nil {
		if target == nil || boundRoute != record.Route || prefix != record.Prefix || target.LocalPrefix != record.Prefix || target.authority != authority || connectSpec(target.Route) != spec || !canonicalConnectRoute(*target) {
			return connectProof{}, connectConflict()
		}
		a, err := readConnectAccount(tx, target.Account)
		if err != nil {
			return connectProof{}, connectConflict()
		}
		if !validConnectAccount(a) {
			return connectProof{}, connectConflict()
		}
		proof := connectProofFor(record, a, target, *target, revision, false, false)
		if record.Action == "reconnect" {
			proof.Action = "reconnect"
			return proof, nil
		}
		if !s.readyConnectAccount(a) {
			return proof, connectProblem{409, "This managed account needs an explicit reconnect request"}
		}
		return proof, nil
	}
	if record.Action == "reconnect" {
		return connectProof{}, connectProblem{409, "Reconnect requires a previously approved managed connection"}
	}
	if target != nil {
		if target.LocalPrefix != record.Prefix || !canonicalConnectRoute(*target) {
			return connectProof{}, connectConflict()
		}
		a, err := readConnectAccount(tx, target.Account)
		if err != nil || !s.readyConnectAccount(a) {
			return connectProof{}, connectConflict()
		}
		return connectProofFor(record, a, target, *target, revision, false, true), nil
	}
	expected, err := expectedConnectRoute(record.Account)
	if err != nil {
		return connectProof{}, err
	}
	if err = validatePrefix(expected.Route, s.snap.routes); err != nil {
		return connectProof{}, connectConflict()
	}
	if s.opts.ConfigFile != "" {
		return connectProof{}, connectProblem{409, "Adding an alias is unavailable while routes are operator-file managed; choose an existing connection name"}
	}
	return s.resolveConnectAlias(tx, record, revision)
}

type connectCandidate struct {
	account accountRecord
	route   *activeRoute
}

func (s *Service) connectCandidates(tx *sql.Tx, accountName string) ([]connectCandidate, int, error) {
	rows, err := tx.Query("SELECT name FROM accounts WHERE kind='claude-code' ORDER BY name")
	if err != nil {
		return nil, 0, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, 0, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	candidates := []connectCandidate{}
	for _, name := range names {
		if accountName != "" && name != accountName {
			continue
		}
		a, e := readConnectAccount(tx, name)
		if e != nil {
			return nil, 0, e
		}
		if !s.readyConnectAccount(a) {
			continue
		}
		for i := range s.snap.routes {
			route := &s.snap.routes[i]
			if route.Account == name && canonicalConnectRoute(*route) {
				candidates = append(candidates, connectCandidate{account: a, route: route})
			}
		}
	}
	return candidates, len(names), nil
}
func (s *Service) resolveConnectAlias(tx *sql.Tx, record connectReceipt, revision int64) (connectProof, error) {
	account, err := readConnectAccount(tx, record.Account)
	if err != nil && err != sql.ErrNoRows {
		return connectProof{}, err
	}
	filter := ""
	if err == nil {
		if !s.readyConnectAccount(account) {
			return connectProof{}, connectConflict()
		}
		filter = account.Name
	}
	candidates, total, err := s.connectCandidates(tx, filter)
	if err != nil {
		return connectProof{}, err
	}
	if len(candidates) > 1 {
		return connectProof{}, connectProblem{409, "Several Claude connections are available; choose an existing connection name"}
	}
	if len(candidates) == 1 {
		candidate := candidates[0]
		alias, e := connectAlias(record, candidate.route, candidate.account.Name)
		if e != nil {
			return connectProof{}, e
		}
		return connectProofFor(record, candidate.account, candidate.route, alias, revision, true, true), nil
	}
	if filter != "" {
		// An explicitly named, valid account may receive a fixed new alias even if
		// it has no route yet. The fresh preview identifies that exact existing slot.
		alias, e := connectAlias(record, nil, account.Name)
		if e != nil {
			return connectProof{}, e
		}
		return connectProofFor(record, account, nil, alias, revision, true, true), nil
	}
	if total != 0 {
		return connectProof{}, connectProblem{409, "Existing Claude credentials need a safe ready connection; choose an existing connection name"}
	}
	alias, e := connectAlias(record, nil, record.Account)
	if e != nil {
		return connectProof{}, e
	}
	proof := connectProofFor(record, accountRecord{AccountInfo: AccountInfo{Name: record.Account}}, nil, alias, revision, true, true)
	proof.Action = "add"
	return proof, nil
}
func sameConnectProof(current, approved connectProof) bool {
	if current.Action != approved.Action || current.SourceAccount != approved.SourceAccount || current.Generation != approved.Generation || current.Authority != approved.Authority || current.Spec != approved.Spec || current.SourceRoute != approved.SourceRoute || current.SourceAuthority != approved.SourceAuthority || current.SourceSpec != approved.SourceSpec || current.AddRoute != approved.AddRoute || current.Adopt != approved.Adopt {
		return false
	}
	return !current.AddRoute || current.Revision == approved.Revision
}
func (s *Service) connectApprovedRoute(record connectReceipt) (activeRoute, error) {
	proof := record.Proof
	if !proof.AddRoute {
		route := s.connectNamedRoute(record.Route)
		if route == nil {
			return activeRoute{}, connectConflict()
		}
		return *route, nil
	}
	var source *activeRoute
	if proof.SourceRoute != "" {
		source = s.connectNamedRoute(proof.SourceRoute)
		if source == nil {
			return activeRoute{}, connectConflict()
		}
	}
	return connectAlias(record, source, proof.SourceAccount)
}
