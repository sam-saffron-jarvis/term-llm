package proxy

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/serveui"
	"gopkg.in/yaml.v3"
)

func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonReply(w, status, map[string]string{"error": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "Use application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConfig))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "Invalid JSON fields or body size")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "Expected one JSON value")
		return false
	}
	return true
}
func (s *Service) browserOrigin(r *http.Request) (string, bool) {
	if s.opts.PublicURL != "" {
		u, _ := url.Parse(s.opts.PublicURL)
		return s.opts.PublicURL, strings.EqualFold(r.Host, u.Host)
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	allowed := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host, allowed
}
func (s *Service) admin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	// Bound slow admin bodies without imposing a short upstream stream deadline.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Second))
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	origin, allowed := s.browserOrigin(r)
	if !allowed {
		fail(w, 403, "Admin Host is not allowed; use loopback or --public-url")
		return
	}
	if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
		fail(w, 403, "Cross-origin administration is not allowed")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != origin {
		fail(w, 403, "A matching Origin is required")
		return
	}
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/_proxy/", http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/_proxy/" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, adminShell)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_proxy/assets/") && r.Method == "GET" {
		name := strings.TrimPrefix(r.URL.Path, "/_proxy/assets/")
		if name != "proxy.js" && name != "proxy.css" {
			http.NotFound(w, r)
			return
		}
		data, err := serveui.StaticAsset("dist/" + name)
		if err != nil {
			fail(w, 503, "Proxy assets missing: run make frontend")
			return
		}
		if name == "proxy.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		w.Write(data)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/_proxy/api/")
	if path == "auth/status" && r.Method == "GET" {
		s.authStatus(w, r)
		return
	}
	if s.passkey != nil && strings.HasPrefix(path, "auth/") {
		s.passkeyAPI(w, r, strings.TrimPrefix(path, "auth/"))
		return
	}
	if s.passkey == nil && path == "login" && r.Method == "POST" {
		s.login(w, r)
		return
	}
	sess, ok := s.getSession(r)
	if !ok {
		fail(w, 401, "Sign in to administer this proxy")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.csrf)) != 1 {
		fail(w, 403, "Session changed; refresh and try again")
		return
	}
	s.adminAPI(w, r, path, sess)
}

const adminShell = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>Credential proxy · term-llm</title><link rel="stylesheet" href="/_proxy/assets/proxy.css"><script type="module" src="/_proxy/assets/proxy.js"></script></head><body><div id="proxy-app"><p>Loading credential proxy…</p></div><noscript>This administration UI requires JavaScript. Forwarding does not.</noscript></body></html>`

func (s *Service) getSession(r *http.Request) (session, bool) {
	if s.passkey != nil {
		s.passkey.mu.Lock()
		defer s.passkey.mu.Unlock()
		return s.passkeySession(r)
	}
	cookie, err := r.Cookie("tlp_admin")
	if err != nil {
		return session{}, false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	key := hashToken(cookie.Value)
	sess, ok := s.sessions[key]
	if !ok || time.Now().After(sess.expires) {
		delete(s.sessions, key)
		return session{}, false
	}
	return sess, true
}
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	// A strong, valid admin credential is never locked out by a bad-attempt
	// bucket shared by clients behind a reverse proxy. Hash/compare is bounded
	// and constant-time; throttling limits only invalid guesses and map growth.
	valid := subtle.ConstantTimeCompare([]byte(hashToken(in.Token)), []byte(s.adminHash)) == 1
	s.authMu.Lock()
	for key, a := range s.attempts {
		if now.Sub(a.since) > time.Minute {
			delete(s.attempts, key)
		}
	}
	if !valid {
		a := s.attempts[peer]
		if a.count >= 8 || (a.count == 0 && len(s.attempts) >= 1024) {
			s.authMu.Unlock()
			w.Header().Set("Retry-After", "60")
			fail(w, 429, "Too many invalid sign-in attempts; wait one minute")
			return
		}
		if a.count == 0 {
			a.since = now
		}
		a.count++
		s.attempts[peer] = a
		s.authMu.Unlock()
		fail(w, 401, "Admin credential not recognized")
		return
	}
	delete(s.attempts, peer)
	s.authMu.Unlock()
	token := randomToken("admin_")
	sess := session{csrf: randomToken("csrf_"), expires: now.Add(8 * time.Hour)}
	s.authMu.Lock()
	for key, v := range s.sessions {
		if now.After(v.expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 64 {
		s.authMu.Unlock()
		fail(w, 429, "Session limit reached; sign out another browser or restart")
		return
	}
	s.sessions[hashToken(token)] = sess
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "tlp_admin", Value: token, Path: "/_proxy/", HttpOnly: true, Secure: s.opts.PublicURL != "" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	jsonReply(w, 200, map[string]string{"csrf": sess.csrf})
}
func (s *Service) adminAPI(w http.ResponseWriter, r *http.Request, path string, sess session) {
	switch {
	case path == "state" && r.Method == "GET":
		s.state(w, sess)
	case path == "config" && r.Method == "PUT":
		var in struct {
			Revision int64                `json:"revision"`
			Routes   []Route              `json:"routes"`
			Secrets  map[string]SecretRef `json:"secrets"`
		}
		if readJSON(w, r, &in) {
			s.updateConfig(w, Config{Routes: in.Routes, Secrets: in.Secrets}, in.Revision)
		}
	case path == "import" && r.Method == "POST":
		var in struct {
			Revision int64  `json:"revision"`
			YAML     string `json:"yaml"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		c, err := decodeConfig([]byte(in.YAML))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.updateConfig(w, c, in.Revision)
	case path == "export" && r.Method == "GET":
		s.mu.RLock()
		b, err := yaml.Marshal(s.snap.config)
		s.mu.RUnlock()
		if err != nil {
			fail(w, 500, "Export unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="proxy.yaml"`)
		w.Write(b)
	case path == "reload" && r.Method == "POST":
		s.reload(w)
	case path == "secrets" && (r.Method == "PUT" || r.Method == "DELETE"):
		s.changeSecret(w, r)
	case path == "callers" && (r.Method == "POST" || r.Method == "PUT"):
		s.changeCaller(w, r)
	case path == "callers" && r.Method == "DELETE":
		var in struct {
			ID string `json:"id"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		s.mu.Lock()
		_, err := s.store.db.Exec("UPDATE callers SET revoked=1 WHERE id=?", in.ID)
		s.mu.Unlock()
		if err != nil {
			fail(w, 503, "Could not revoke caller")
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
	case (path == "requests" || path == "requests/export") && r.Method == "GET":
		s.requests(w, r, path == "requests/export")
	case path == "logout" && r.Method == "POST":
		if s.passkey != nil {
			s.passkeyLogout(w, r, sess)
			return
		}
		if c, err := r.Cookie("tlp_admin"); err == nil {
			s.authMu.Lock()
			delete(s.sessions, hashToken(c.Value))
			s.authMu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "tlp_admin", Path: "/_proxy/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonReply(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "Unknown administration endpoint or method")
	}
}
func (s *Service) state(w http.ResponseWriter, sess session) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	callers, err := s.store.callers()
	if err != nil {
		fail(w, 503, "Could not read caller state")
		return
	}
	type secretInfo struct {
		Name   string `json:"name"`
		Origin string `json:"origin"`
		Source string `json:"source"`
	}
	secrets := []secretInfo{}
	seen := map[string]bool{}
	for name, m := range s.snap.secrets {
		source := "stored"
		if s.snap.config.Secrets[name].File != "" {
			source = "file"
		}
		secrets = append(secrets, secretInfo{name, m.Origin, source})
		seen[name] = true
	}
	// Include unreferenced stored entries in operator mode without activating them.
	stored, err := s.store.materials()
	if err != nil {
		fail(w, 503, "Could not read secret metadata")
		return
	}
	for name, m := range stored {
		if !seen[name] {
			secrets = append(secrets, secretInfo{name, m.Origin, "stored"})
		}
	}
	mode := "ui"
	if s.opts.ConfigFile != "" {
		mode = "file"
	}
	jsonReply(w, 200, map[string]any{"mode": mode, "auth": s.opts.Auth, "revision": s.snap.revision, "routes": s.snap.config.Routes, "secrets": secrets, "callers": callers, "csrf": sess.csrf, "retention_days": s.opts.RetentionDays})
}
func (s *Service) updateConfig(w http.ResponseWriter, c Config, revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.ConfigFile != "" {
		fail(w, 409, "Routes are operator-managed; edit the mounted file and Reload")
		return
	}
	if revision != s.snap.revision {
		fail(w, 409, "Configuration changed in another tab; refresh before saving")
		return
	}
	stored, err := s.store.materials()
	if err != nil {
		fail(w, 503, "Could not read secret store")
		return
	}
	secrets, err := s.resolveSecrets(c, stored, false)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	next, err := compile(c, revision+1, secrets)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err = s.store.saveConfig(next, false); err != nil {
		fail(w, 503, "Could not commit configuration; active routes unchanged")
		return
	}
	s.snap = next
	jsonReply(w, 200, map[string]int64{"revision": next.revision})
}
func (s *Service) reload(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.ConfigFile == "" {
		fail(w, 409, "No operator file configured")
		return
	}
	c, err := fileConfig(s.opts.ConfigFile)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	stored, err := s.store.materials()
	if err != nil {
		fail(w, 503, "Could not read secret store")
		return
	}
	secrets, err := s.resolveSecrets(c, stored, true)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	next, err := compile(c, s.snap.revision+1, secrets)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err = s.store.saveConfig(next, true); err != nil {
		fail(w, 503, "Could not commit reload; active routes unchanged")
		return
	}
	s.snap = next
	jsonReply(w, 200, map[string]int64{"revision": next.revision})
}
func (s *Service) changeCaller(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string   `json:"id,omitempty"`
		Name     string   `json:"name,omitempty"`
		Grants   []string `json:"grants"`
		Revision *int64   `json:"revision,omitempty"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Grants) > 128 || (r.Method == "POST" && (len(in.Name) < 1 || len(in.Name) > 80)) {
		fail(w, 400, "Enter a caller name (1–80 characters) and at most 128 explicit route grants")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Revision != nil && *in.Revision != s.snap.revision {
		fail(w, 409, "Route configuration changed; refresh before granting access")
		return
	}
	if r.Method == "PUT" {
		if err := s.store.updateGrants(in.ID, in.Grants, s.snap); err != nil {
			callerFailure(w, err)
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	caller, token, err := s.store.createCaller(in.Name, in.Grants, s.snap)
	if err != nil {
		callerFailure(w, err)
		return
	}
	jsonReply(w, 201, map[string]any{"caller": caller, "token": token})
}
func callerFailure(w http.ResponseWriter, err error) {
	if safe, ok := err.(inputError); ok {
		fail(w, 400, safe.Error())
		return
	}
	fail(w, 503, "Could not commit caller grants; existing access is unchanged")
}
func (s *Service) requests(w http.ResponseWriter, r *http.Request, export bool) {
	limit := 50
	if export {
		limit = 1000
	}
	query := r.URL.Query()
	query.Del("_active")
	if query.Get("outcome") == "active" || query.Get("outcome") == "unknown" {
		s.active.Range(func(key, _ any) bool { query.Add("_active", key.(string)); return true })
	}
	records, next, err := s.store.records(query, limit)
	if err != nil {
		fail(w, 400, "Could not read requests: check filters or store readiness")
		return
	}
	for i := range records {
		if records[i].Ended == 0 {
			if _, ok := s.active.Load(records[i].ID); ok {
				records[i].Outcome = "active"
			} else {
				records[i].Outcome = "unknown"
			}
		}
	}
	if export {
		w.Header().Set("Content-Disposition", `attachment; filename="proxy-requests.json"`)
	}
	jsonReply(w, 200, map[string]any{"requests": records, "next": next})
}

type secretInput struct {
	Name    string `json:"name"`
	Origin  string `json:"origin,omitempty"`
	Value   string `json:"value,omitempty"`
	Replace bool   `json:"replace,omitempty"`
}

func (s *Service) changeSecret(w http.ResponseWriter, r *http.Request) {
	var in secretInput
	if !readJSON(w, r, &in) {
		return
	}
	if !slug.MatchString(in.Name) {
		fail(w, 400, "Secret name must be a slug (up to 64 characters)")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.materials()
	if err != nil {
		fail(w, 503, "Could not read secret store")
		return
	}
	if s.snap.config.Secrets[in.Name].File != "" {
		fail(w, 409, "File secrets can only be changed by the operator")
		return
	}
	if status, err := s.prepareSecretChange(in, r.Method == "DELETE", stored); err != nil {
		fail(w, status, err.Error())
		return
	}
	secrets, err := s.updatedSecretMaterials(stored)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	next, err := compile(s.snap.config, s.snap.revision+1, secrets)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		fail(w, 503, "Could not begin secret update")
		return
	}
	defer tx.Rollback()
	if r.Method == "DELETE" {
		_, err = tx.Exec("DELETE FROM secrets WHERE name=?", in.Name)
	} else {
		m := stored[in.Name]
		if in.Replace {
			_, err = tx.Exec("UPDATE secrets SET value=? WHERE name=? AND origin=?", m.Value, in.Name, m.Origin)
		} else {
			_, err = tx.Exec("INSERT INTO secrets VALUES(?,?,?)", in.Name, m.Origin, m.Value)
		}
	}
	if err == nil {
		_, err = tx.Exec("UPDATE configuration SET revision=? WHERE id=1", next.revision)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "Could not commit secret update; active credentials unchanged")
		return
	}
	s.snap = next
	jsonReply(w, 200, map[string]int64{"revision": next.revision})
}

func (s *Service) prepareSecretChange(in secretInput, deleting bool, stored map[string]material) (int, error) {
	if deleting {
		for _, route := range s.snap.routes {
			for _, rule := range route.SetHeaders {
				ref := s.snap.config.Secrets[rule.Secret]
				if rule.Secret == in.Name || ref.Stored == in.Name {
					return 409, inputError("Secret is referenced by a route; remove its header rule first")
				}
			}
		}
		delete(stored, in.Name)
	} else {
		_, exists := stored[in.Name]
		if exists && !in.Replace {
			return 409, inputError("Secret name already exists; use Replace to rotate its value explicitly")
		}
		if !exists && in.Replace {
			return 409, inputError("Secret no longer exists; refresh before replacing it")
		}
		origin, e := secretOrigin(in.Origin)
		if e != nil || (in.Value == "" || len(in.Value) > 16<<10 || strings.ContainsAny(in.Value, "\r\n\x00")) {
			return 400, inputError("Enter a valid upstream origin and a nonempty single-line secret (maximum 16 KiB)")
		}
		if old, ok := stored[in.Name]; ok && old.Origin != origin {
			return 409, inputError("Existing secrets cannot change origin; use a new secret name")
		}
		if len(stored) >= 256 && stored[in.Name].Value == "" {
			return 409, inputError("Secret limit reached")
		}
		stored[in.Name] = material{Origin: origin, Value: in.Value}
	}
	return 0, nil
}

// Stored-secret edits do not implicitly reload operator-owned file credentials.
// Their already validated bindings/bytes remain active until explicit Reload.
func (s *Service) updatedSecretMaterials(stored map[string]material) (map[string]material, error) {
	c := s.snap.config
	if s.opts.ConfigFile == "" {
		return s.resolveSecrets(c, stored, false)
	}
	c.Secrets = make(map[string]SecretRef)
	for name, ref := range s.snap.config.Secrets {
		if ref.File == "" {
			c.Secrets[name] = ref
		}
	}
	materials, err := s.resolveSecrets(c, stored, true)
	if err != nil {
		return nil, err
	}
	for name, ref := range s.snap.config.Secrets {
		if ref.File != "" {
			materials[name] = s.snap.secrets[name]
		}
	}
	return materials, nil
}
