package proxy

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/passkeyauth"
)

func (s *Service) authStatus(w http.ResponseWriter, r *http.Request) {
	if s.passkey == nil {
		jsonReply(w, 200, map[string]any{"auth": "token", "csrf": "", "origin": s.opts.PublicURL})
		return
	}
	p := s.passkey
	p.mu.Lock()
	defer p.mu.Unlock()
	csrf := cookieValue(r, "tlp_preauth")
	if len(csrf) != 64 {
		csrf = randomToken("")
		s.authCookie(w, "tlp_preauth", csrf, 600)
	}
	if sess, ok := s.passkeySession(r); ok {
		csrf = sess.csrf
	}
	_, bootstrapErr := p.bootstrap.Authenticate(cookieValue(r, "tlp_bootstrap"))
	_, recoveryErr := p.recovery.Authenticate(cookieValue(r, "tlp_recovery"))
	first := p.store.CredentialCount() == 0
	bootstrapVerified := first && bootstrapErr == nil
	recoveryVerified := !first && recoveryErr == nil
	jsonReply(w, 200, map[string]any{"auth": "passkey", "csrf": csrf, "origin": p.endpoint.Origin, "setup_required": first,
		"bootstrap_available": first && (p.bootstrap.Enabled() || bootstrapVerified), "recovery_available": !first && (p.recovery.Enabled() || recoveryVerified),
		"bootstrap_verified": bootstrapVerified, "recovery_verified": recoveryVerified})
}

func (s *Service) passkeyAPI(w http.ResponseWriter, r *http.Request, path string) {
	p := s.passkey
	p.mu.Lock()
	defer p.mu.Unlock()
	sess, authenticated := s.passkeySession(r)
	public := path == "bootstrap/verify" || path == "bootstrap/begin" || path == "bootstrap/finish" || path == "recovery/verify" || path == "recovery/begin" || path == "recovery/finish" || path == "login/begin" || path == "login/finish"
	if !public && !authenticated {
		fail(w, 401, "Sign in to manage passkeys")
		return
	}
	if r.Method != "GET" {
		csrf := cookieValue(r, "tlp_preauth")
		if authenticated {
			csrf = sess.csrf
		}
		if csrf == "" || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			fail(w, 403, "Refresh authentication before trying again")
			return
		}
	}
	switch {
	case r.Method == "GET" && path == "credentials":
		s.passkeyCredentials(w)
	case r.Method == "DELETE" && path == "credentials":
		s.deletePasskey(w, r, sess)
	case r.Method == "POST" && path == "sessions/revoke":
		if !s.consumeRecentAuth(w, sess) {
			return
		}
		if _, err := p.sessions.RevokeOthers(sess.principal); err != nil {
			fail(w, 401, "Session is no longer valid")
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
	case r.Method == "POST":
		s.passkeyCeremonyAPI(w, r, path, sess)
	default:
		fail(w, 404, "Unknown authentication endpoint or method")
	}
}
func (s *Service) passkeyCeremonyAPI(w http.ResponseWriter, r *http.Request, path string, sess session) {
	action, step, ok := strings.Cut(path, "/")
	if !ok {
		fail(w, 404, "Unknown authentication endpoint")
		return
	}
	switch action {
	case "bootstrap", "recovery":
		if step == "verify" {
			s.verifyPasskeyGrant(w, r, action)
			return
		}
		fallthrough
	case "enroll":
		switch step {
		case "begin":
			s.beginPasskeyRegistration(w, r, action, sess)
		case "finish":
			s.finishPasskeyRegistration(w, r, action, sess)
		default:
			fail(w, 404, "Unknown enrollment endpoint")
		}
	case "login", "reauth":
		switch step {
		case "begin":
			s.beginPasskeyLogin(w, r, action, sess)
		case "finish":
			s.finishPasskeyLogin(w, r, action, sess)
		default:
			fail(w, 404, "Unknown sign-in endpoint")
		}
	default:
		fail(w, 404, "Unknown authentication endpoint")
	}
}
func (s *Service) verifyPasskeyGrant(w http.ResponseWriter, r *http.Request, action string) {
	var in struct {
		Token string `json:"token"`
	}
	if !readAuthJSON(w, r, &in) {
		return
	}
	p := s.passkey
	grants := p.bootstrap
	if action == "recovery" {
		grants = p.recovery
	}
	if (action == "bootstrap") != (p.store.CredentialCount() == 0) {
		fail(w, 409, "Enrollment state changed; refresh")
		return
	}
	grant, err := grants.Verify([]byte(in.Token))
	if err != nil {
		if s.limitBadHostSecret(w, r, action) {
			return
		}
		fail(w, 401, "Host secret is invalid, expired or already exchanged; restart with a fresh private file")
		return
	}
	s.authCookie(w, "tlp_"+action, grant.Token, int(passkeyauth.GrantSessionLifetime/time.Second))
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) passkeyCredentials(w http.ResponseWriter) {
	type credential struct {
		ID       string    `json:"id"`
		Name     string    `json:"name"`
		Created  time.Time `json:"created_at"`
		LastUsed time.Time `json:"last_used_at"`
	}
	list := []credential{}
	for _, c := range s.passkey.store.Credentials() {
		list = append(list, credential{c.RecordID, c.DisplayName, c.CreatedAt, c.LastUsedAt})
	}
	jsonReply(w, 200, map[string]any{"credentials": list, "sessions": s.passkey.sessions.Count()})
}
func (s *Service) consumeRecentAuth(w http.ResponseWriter, sess session) bool {
	if s.passkey.sessions.ConsumeRecentAuth(sess.principal) != nil {
		fail(w, 403, "Confirm with your passkey before this security change")
		return false
	}
	return true
}
func (s *Service) deletePasskey(w http.ResponseWriter, r *http.Request, sess session) {
	var in struct {
		ID string `json:"id"`
	}
	if !readAuthJSON(w, r, &in) || !s.consumeRecentAuth(w, sess) {
		return
	}
	if _, err := s.passkey.store.DeleteCredential(in.ID); err != nil {
		fail(w, 409, "Cannot remove this passkey; keep at least one enrolled passkey")
		return
	}
	_, _ = s.passkey.sessions.RevokeCredential(in.ID)
	if in.ID == sess.principal.CredentialRecordID {
		s.authCookie(w, "tlp_admin", "", -1)
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

// Bound invalid host-secret attempts/maps without allowing another browser
// sharing the TLS terminator's peer to lock out a valid, strong private secret.
func (s *Service) limitBadHostSecret(w http.ResponseWriter, r *http.Request, action string) bool {
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := action + ":" + peer
	now := time.Now()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	for k, a := range s.attempts {
		if now.Sub(a.since) > time.Minute {
			delete(s.attempts, k)
		}
	}
	a := s.attempts[key]
	if a.count >= 8 || (a.count == 0 && len(s.attempts) >= 1024) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Too many invalid host secrets; wait one minute")
		return true
	}
	if a.count == 0 {
		a.since = now
	}
	a.count++
	s.attempts[key] = a
	return false
}
