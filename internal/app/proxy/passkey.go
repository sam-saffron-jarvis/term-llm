package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/term-llm/term-llm/internal/filelock"
	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/passkeyauth"
)

// proxyPasskeys is a standalone HTTP adapter, not a serve/Hub runtime. Protocol,
// credential validation, grants, ceremonies and sessions belong to passkeyauth.
// Sessions deliberately remain in memory: a proxy restart signs out all browsers.
type proxyPasskeys struct {
	mu                  sync.Mutex
	endpoint            passkeyauth.Endpoint
	store               *passkeyauth.Store
	rp                  *passkeyauth.RelyingParty
	sessions            *passkeyauth.Sessions
	ceremonies          *passkeyauth.Ceremonies
	logins              *passkeyauth.Ceremonies // isolated, replaceable unauthenticated capacity
	bootstrap, recovery *passkeyauth.Grants
	unlock              func() error
}

func prepareAuthOptions(opts *Options) (string, error) {
	if opts.StateDir == "" {
		return "", fmt.Errorf("--state-dir is required")
	}
	if opts.Auth == "" {
		opts.Auth = "passkey"
	}
	switch opts.Auth {
	case "passkey":
		if opts.AdminTokenFile != "" {
			return "", fmt.Errorf("--admin-token-file requires explicit --auth token; there is no token bypass in passkey mode")
		}
		u, err := url.Parse(opts.PublicURL)
		if err != nil || u == nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
			return "", fmt.Errorf("--public-url must be an exact origin, without a mount path")
		}
		// ParseEndpoint accepts a cookie mount, but proxy forwarding owns the root.
		// Pin WebAuthn to the origin and scope all browser cookies to /_proxy/.
		raw := strings.TrimSuffix(opts.PublicURL, "/") + "/_proxy/"
		endpoint, err := passkeyauth.ParseEndpoint(passkeyauth.EndpointOptions{PublicURL: raw})
		if err != nil {
			return "", err
		}
		opts.PublicURL = endpoint.Origin
		return "", nil
	case "token":
		if opts.PasskeyBootstrapTokenFile != "" || opts.PasskeyRecoveryTokenFile != "" {
			return "", fmt.Errorf("passkey enrollment/recovery files require --auth passkey")
		}
		if opts.AdminTokenFile == "" {
			return "", fmt.Errorf("--admin-token-file is required with --auth token")
		}
		b, err := privateFile(opts.AdminTokenFile, 4096)
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(b))
		if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
			return "", fmt.Errorf("admin token must have at least 32 non-whitespace characters; provision 256 random bits")
		}
		if opts.PublicURL != "" {
			u, err := url.Parse(opts.PublicURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
				return "", fmt.Errorf("public URL must be an HTTPS origin")
			}
			host := strings.ToLower(u.Hostname())
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			if port := u.Port(); port != "" && port != "443" {
				host = net.JoinHostPort(u.Hostname(), port)
			}
			opts.PublicURL = "https://" + host
		}
		return hashToken(token), nil
	default:
		return "", fmt.Errorf("--auth must be passkey or token")
	}
}

func openProxyPasskeys(opts Options) (_ *proxyPasskeys, err error) {
	endpoint, err := passkeyauth.ParseEndpoint(passkeyauth.EndpointOptions{PublicURL: opts.PublicURL + "/_proxy/"})
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(opts.StateDir, "admin-auth")
	if err = fileprivacy.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("create proxy passkey directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect proxy passkey directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("passkey directory must be a non-symlink directory")
	}
	if err = fileprivacy.CheckDir(info); err != nil {
		return nil, fmt.Errorf("passkey directory must be private (0700): %w", err)
	}
	lockPath := filepath.Join(dir, "auth.lock")
	if st, statErr := os.Lstat(lockPath); statErr == nil {
		if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || fileprivacy.CheckFile(st) != nil {
			return nil, fmt.Errorf("passkey lock must be a private regular non-symlink file")
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect passkey lock: %w", statErr)
	}
	unlock, err := filelock.TryLock(lockPath)
	if err != nil {
		return nil, fmt.Errorf("lock proxy passkey state: %w", err)
	}
	defer func() {
		if err != nil {
			_ = unlock()
		}
	}()
	store, err := passkeyauth.OpenStore(passkeyauth.StoreOptions{Path: filepath.Join(dir, "credentials.json"), RPID: endpoint.RPID, UserName: "proxy-admin"})
	if err != nil {
		return nil, err
	}
	first := store.CredentialCount() == 0
	if first && opts.PasskeyBootstrapTokenFile == "" {
		return nil, fmt.Errorf("first passkey requires --passkey-bootstrap-token-file (private random secret)")
	}
	if first && opts.PasskeyRecoveryTokenFile != "" {
		return nil, fmt.Errorf("recovery requires an enrolled passkey; use bootstrap for first enrollment")
	}
	bootstrap, err := proxyHostGrant(passkeyauth.GrantBootstrap, opts.PasskeyBootstrapTokenFile, first)
	if err != nil {
		return nil, err
	}
	recovery, err := proxyHostGrant(passkeyauth.GrantRecovery, opts.PasskeyRecoveryTokenFile, !first)
	if err != nil {
		return nil, err
	}
	rp, err := passkeyauth.NewRelyingParty(passkeyauth.RelyingPartyOptions{Endpoint: endpoint, DisplayName: "Credential proxy"})
	if err != nil {
		return nil, err
	}
	return &proxyPasskeys{endpoint: endpoint, store: store, rp: rp, bootstrap: bootstrap, recovery: recovery, sessions: passkeyauth.NewSessions(nil, nil), ceremonies: passkeyauth.NewCeremonies(nil, nil), logins: passkeyauth.NewCeremonies(nil, nil), unlock: unlock}, nil
}

func proxyHostGrant(kind passkeyauth.GrantKind, path string, enabled bool) (*passkeyauth.Grants, error) {
	var secret []byte
	if path != "" && enabled {
		// Bound before the shared private-file reader; never expose contents/errors.
		info, err := os.Lstat(path)
		if err != nil || info.Size() > 4096 {
			return nil, fmt.Errorf("passkey %s file unavailable or too large", kind)
		}
		secret, err = passkeyauth.ReadPrivateSecretFile(path)
		if err != nil {
			return nil, fmt.Errorf("passkey %s file must contain a private non-symlink host secret", kind)
		}
		defer clear(secret)
	}
	return passkeyauth.NewGrants(kind, secret, nil, nil)
}

func (s *Service) authCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/_proxy/", HttpOnly: true, Secure: s.passkey.endpoint.Secure, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func sessionCSRF(token string) string { return hashToken("proxy-session-csrf:" + token) }

// Caller holds passkey.mu so revocation/recovery cannot race authentication.
func (s *Service) passkeySession(r *http.Request) (session, bool) {
	p := s.passkey
	token := cookieValue(r, "tlp_admin")
	principal, err := p.sessions.Authenticate(token)
	if err != nil || !p.store.HasCredential(principal.CredentialRecordID) {
		return session{}, false
	}
	return session{csrf: sessionCSRF(token), principal: principal}, true
}
func (s *Service) passkeyLogout(w http.ResponseWriter, r *http.Request, sess session) {
	p := s.passkey
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.sessions.Logout(sess.principal)
	s.authCookie(w, "tlp_admin", "", -1)
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) issuePasskeySession(w http.ResponseWriter, recordID string) {
	issued, err := s.passkey.sessions.Create(recordID)
	if err != nil {
		fail(w, 503, "Could not create browser session")
		return
	}
	s.authCookie(w, "tlp_admin", issued.Token, int(passkeyauth.SessionAbsoluteLifetime/time.Second))
	jsonReply(w, 200, map[string]string{"csrf": sessionCSRF(issued.Token)})
}
