package proxy

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/buildinfo"
)

type accountInput struct {
	Name        string             `json:"name"`
	Kind        string             `json:"kind,omitempty"`
	Region      string             `json:"region,omitempty"`
	Replace     bool               `json:"replace,omitempty"`
	Credentials accountCredentials `json:"credentials,omitempty"`
}

func (s *Service) accountsAPI(w http.ResponseWriter, r *http.Request, sess session) {
	if r.Method == http.MethodGet {
		infos, err := s.store.accountInfos()
		if err != nil {
			fail(w, 503, "Could not read account metadata")
			return
		}
		jsonReply(w, 200, map[string]any{"accounts": infos})
		return
	}
	var in accountInput
	if !readJSON(w, r, &in) {
		return
	}
	if !slug.MatchString(in.Name) {
		fail(w, 400, "Account name must be a slug")
		return
	}
	if s.passkey != nil && !s.consumeRecentAuth(w, sess) {
		return
	}
	unlock, lockErr := s.accountLock(r.Context(), in.Name)
	if lockErr != nil {
		fail(w, 503, "Account operation canceled")
		return
	}
	defer unlock()
	// No issuer requests under the global configuration lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.store.account(in.Name)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Could not read account")
		return
	}
	if r.Method == http.MethodDelete {
		if !exists {
			fail(w, 404, "Account not found")
			return
		}
		next := old
		next.credentials = accountCredentials{}
		next.identityVersion++
		next.State = "needs_login"
		next.Generation++
		next.ExpiresAt = 0
		next.Updated = time.Now().UnixMilli()
		next.endpoint = ""
		if err = s.store.replaceAccount(r.Context(), next, old.Generation); err != nil {
			fail(w, 503, "Could not durably sign out account")
			return
		}
		s.cancelAccountSockets(in.Name)
		jsonReply(w, 200, map[string]any{"account": next.AccountInfo})
		return
	}
	if exists && (r.Method == http.MethodPost || !in.Replace) {
		fail(w, 409, "Account exists; explicitly replace it")
		return
	}
	if !exists && r.Method == http.MethodPut {
		fail(w, 404, "Account not found")
		return
	}
	if exists && in.Kind == "" {
		in.Kind = old.Kind
	}
	if exists && old.Kind != in.Kind {
		fail(w, 409, "Account kind is immutable; use a new name")
		return
	}
	if err = validateAccountCredentials(in.Kind, in.Credentials); err != nil {
		fail(w, 400, err.Error())
		return
	}
	next := accountRecord{AccountInfo: AccountInfo{Name: in.Name, Kind: in.Kind, State: "ready", Generation: 1, ExpiresAt: in.Credentials.ExpiresAt, Updated: time.Now().UnixMilli()}, credentials: in.Credentials}
	if in.Kind == "aws-sigv4" && in.Region != "" {
		next.operator.Region = in.Region
	}
	expected := int64(0)
	if exists {
		region := next.operator.Region
		next.operator = old.operator
		if region != "" {
			next.operator.Region = region
		}
		next.Generation = old.Generation + 1
		next.identityVersion = old.identityVersion + 1
		expected = old.Generation
	}
	// AWS region is operator-selected, never taken from a worker signing header.
	if in.Kind == "aws-sigv4" && !validAWSRegion(next.operator.Region) {
		fail(w, 400, "AWS signing accounts require an explicit region")
		return
	}
	next.AuthMode = accountAuthMode(next.Kind, next.credentials)
	if err = s.store.replaceAccount(r.Context(), next, expected); err != nil {
		fail(w, 503, "Could not durably store account")
		return
	}
	s.rememberAccount(next.Name, next.credentials)
	s.cancelAccountSockets(in.Name)
	status := 200
	if !exists {
		status = 201
	}
	jsonReply(w, status, map[string]any{"account": next.AccountInfo})
}

// whoami is caller-token-only and deliberately matched before the browser admin
// handler. Browser cookies, CSRF grants and token issuer operations play no role.
func (s *Service) whoami(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		http.Error(w, "proxy concurrency limit", 503)
		return
	}
	defer func() { <-s.slots }()
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		http.Error(w, "unauthorized", 401)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id, name string
	if s.closing || s.store.db.QueryRow("SELECT id,name FROM callers WHERE hash=? AND revoked=0", hashToken(strings.TrimPrefix(values[0], "Bearer "))).Scan(&id, &name) != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	type info struct {
		Name         string `json:"name"`
		LocalPrefix  string `json:"local_prefix"`
		WebSocket    bool   `json:"websocket"`
		AccountState string `json:"account_state,omitempty"`
	}
	routes := []info{}
	for _, route := range s.snap.routes {
		if !route.Enabled {
			continue
		}
		var authority string
		if s.store.db.QueryRow("SELECT authority FROM grants WHERE caller=? AND route=?", id, route.Name).Scan(&authority) != nil || authority != route.authority {
			continue
		}
		entry := info{Name: route.Name, LocalPrefix: route.LocalPrefix, WebSocket: route.WebSocket}
		if route.Account != "" {
			a, e := s.store.account(route.Account)
			entry.AccountState = "unavailable"
			if e == nil {
				entry.AccountState = a.State
			}
		}
		routes = append(routes, entry)
	}
	jsonReply(w, 200, map[string]any{"name": name, "routes": routes, "version": buildinfo.Version})
}
