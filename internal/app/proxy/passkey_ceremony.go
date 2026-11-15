package proxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/term-llm/term-llm/internal/passkeyauth"
)

const authBodyLimit = 64 << 10

func readAuthJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, authBodyLimit)
	return readJSON(w, r, dst)
}

// Validate the typed browser response once, then let the shared WebAuthn engine
// parse the same bounded body. No provider/request payload enters this adapter.
func replayAuthJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, authBodyLimit+1))
	if err != nil || len(data) > authBodyLimit {
		fail(w, 400, "Invalid authentication body or size")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	if !readAuthJSON(w, r, dst) {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	return true
}
func registrationKind(action string) passkeyauth.CeremonyKind {
	switch action {
	case "bootstrap":
		return passkeyauth.CeremonyBootstrap
	case "recovery":
		return passkeyauth.CeremonyRecovery
	default:
		return passkeyauth.CeremonyAddCredential
	}
}
func (s *Service) registrationBinding(w http.ResponseWriter, r *http.Request, action string, sess session) (grantID, sessionID string, ok bool) {
	p := s.passkey
	if action == "enroll" {
		if !p.sessions.HasRecentAuth(sess.principal) {
			fail(w, 403, "Confirm with your passkey before enrollment")
			return "", "", false
		}
		return "", sess.principal.SessionID, true
	}
	if (action == "bootstrap") != (p.store.CredentialCount() == 0) {
		fail(w, 409, "Enrollment state changed; refresh")
		return "", "", false
	}
	grants := p.bootstrap
	if action == "recovery" {
		grants = p.recovery
	}
	id, err := grants.Authenticate(cookieValue(r, "tlp_"+action))
	if err != nil {
		fail(w, 401, "Verify the private host secret before enrollment")
		return "", "", false
	}
	return id, "", true
}
func (s *Service) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request, action string, sess session) {
	grantID, sessionID, ok := s.registrationBinding(w, r, action, sess)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readAuthJSON(w, r, &in) {
		return
	}
	name, err := passkeyauth.NormalizeCredentialName(in.Name)
	if err != nil {
		fail(w, 400, "Use a short nonempty passkey name")
		return
	}
	user := s.passkey.store.User()
	options, data, err := s.passkey.rp.BeginRegistration(user)
	if err != nil {
		fail(w, 503, "Could not begin passkey enrollment")
		return
	}
	ceremony, err := s.passkey.ceremonies.Create(registrationKind(action), r.RemoteAddr, grantID, sessionID, name, user.WebAuthnID(), data)
	if err != nil {
		fail(w, 429, "Too many pending passkey ceremonies; wait five minutes")
		return
	}
	s.authCookie(w, "tlp_ceremony", ceremony.CookieToken, 300)
	jsonReply(w, 200, options)
}
func (s *Service) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request, action string, sess session) {
	grantID, sessionID, ok := s.registrationBinding(w, r, action, sess)
	if !ok {
		return
	}
	p := s.passkey
	ceremony, err := p.ceremonies.Consume(cookieValue(r, "tlp_ceremony"), registrationKind(action), grantID, sessionID)
	if err != nil {
		fail(w, 401, "Enrollment challenge expired or changed; start again")
		return
	}
	s.authCookie(w, "tlp_ceremony", "", -1)
	user := p.store.User()
	if !bytes.Equal(ceremony.UserHandle, user.WebAuthnID()) {
		fail(w, 401, "Enrollment identity changed")
		return
	}
	var response protocol.CredentialCreationResponse
	if !replayAuthJSON(w, r, &response) {
		return
	}
	raw, err := p.rp.FinishRegistration(user, ceremony.Data, r)
	if err != nil {
		fail(w, 401, "Passkey enrollment failed; start again")
		return
	}
	if action == "enroll" && !s.consumeRecentAuth(w, sess) {
		return
	}
	var saved passkeyauth.Credential
	if action == "bootstrap" {
		saved, err = p.store.CommitFirstCredential(raw, ceremony.Meta)
	} else {
		saved, err = p.store.AddCredential(raw, ceremony.Meta)
	}
	if err != nil {
		fail(w, 409, "Could not save passkey; it may already be enrolled")
		return
	}
	if action == "enroll" {
		jsonReply(w, 200, map[string]string{"csrf": sess.csrf})
		return
	}
	grants := p.bootstrap
	if action == "recovery" {
		grants = p.recovery
	}
	_ = grants.Consume(cookieValue(r, "tlp_"+action))
	s.authCookie(w, "tlp_"+action, "", -1)
	if action == "recovery" {
		// Host-assisted recovery adds a key, not a permanent authentication bypass.
		// Revoke every browser and pending ceremony, then require passkey login.
		p.sessions = passkeyauth.NewSessions(nil, nil)
		p.ceremonies = passkeyauth.NewCeremonies(nil, nil)
		p.logins = passkeyauth.NewCeremonies(nil, nil)
		s.authCookie(w, "tlp_admin", "", -1)
		jsonReply(w, 200, map[string]string{"csrf": cookieValue(r, "tlp_preauth")})
		return
	}
	s.issuePasskeySession(w, saved.RecordID)
}
func loginKind(action string) passkeyauth.CeremonyKind {
	if action == "reauth" {
		return passkeyauth.CeremonyReauth
	}
	return passkeyauth.CeremonyLogin
}
func (s *Service) beginPasskeyLogin(w http.ResponseWriter, r *http.Request, action string, sess session) {
	var in struct{}
	if !readAuthJSON(w, r, &in) {
		return
	}
	p := s.passkey
	user := p.store.User()
	if len(user.Credentials) == 0 {
		fail(w, 409, "Enroll the first passkey before signing in")
		return
	}
	options, data, err := p.rp.BeginLogin(user)
	if err != nil {
		fail(w, 503, "Could not begin passkey sign-in")
		return
	}
	sessionID := ""
	if action == "reauth" {
		sessionID = sess.principal.SessionID
	}
	create := p.ceremonies.Create
	peer := r.RemoteAddr
	if action == "login" {
		create = p.logins.CreateReplacingOldest
		// Caddy clients share an address, not a pre-auth browser principal. Avoid
		// a colon: the shared engine normalizes host:port peers by stripping ports.
		peer = "preauth-" + hashToken(cookieValue(r, "tlp_preauth"))
	}
	ceremony, err := create(loginKind(action), peer, "", sessionID, "", user.WebAuthnID(), data)
	if err != nil {
		fail(w, 429, "Too many pending passkey ceremonies; wait five minutes")
		return
	}
	s.authCookie(w, "tlp_ceremony", ceremony.CookieToken, 300)
	jsonReply(w, 200, options)
}
func (s *Service) finishPasskeyLogin(w http.ResponseWriter, r *http.Request, action string, sess session) {
	p := s.passkey
	sessionID := ""
	if action == "reauth" {
		sessionID = sess.principal.SessionID
	}
	registry := p.ceremonies
	if action == "login" {
		registry = p.logins
	}
	ceremony, err := registry.Consume(cookieValue(r, "tlp_ceremony"), loginKind(action), "", sessionID)
	if err != nil {
		fail(w, 401, "Sign-in challenge expired or changed; start again")
		return
	}
	s.authCookie(w, "tlp_ceremony", "", -1)
	user := p.store.User()
	if !bytes.Equal(ceremony.UserHandle, user.WebAuthnID()) {
		fail(w, 401, "Sign-in identity changed")
		return
	}
	var response protocol.CredentialAssertionResponse
	if !replayAuthJSON(w, r, &response) {
		return
	}
	raw, err := p.rp.FinishLogin(user, ceremony.Data, r)
	if err != nil {
		fail(w, 401, "Passkey sign-in failed; start again")
		return
	}
	count := raw.Authenticator.SignCount
	if data := []byte(response.AssertionResponse.AuthenticatorData); len(data) >= 37 {
		count = binary.BigEndian.Uint32(data[33:37])
	}
	saved, err := p.store.UpdateAfterAssertion(raw, count)
	if err != nil {
		fail(w, 401, "Passkey is no longer valid")
		return
	}
	if action == "reauth" {
		if err = p.sessions.GrantRecentAuth(sess.principal); err != nil {
			fail(w, 401, "Session is no longer valid")
			return
		}
		jsonReply(w, 200, map[string]string{"csrf": sess.csrf})
		return
	}
	s.issuePasskeySession(w, saved.RecordID)
}
