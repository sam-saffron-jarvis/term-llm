package cmd

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

func (s *hubServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHubHealth)
	mux.HandleFunc("/api/nodes/test", s.handleTestNode)
	mux.HandleFunc("/api/nodes/", s.handleNodeItem)
	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/delegations/", s.handleDelegationItem)
	mux.HandleFunc("/api/delegations", s.handleDelegations)
	mux.HandleFunc("/api/connect", s.handleReverseConnect)
	mux.HandleFunc("/node/", s.handleNodeProxy)
	mux.HandleFunc("/", s.handleIndex)
	return s.auth(mux)
}

func (s *hubServer) auth(next http.Handler) http.Handler {
	if !s.requireAuth {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/healthz" || hubNodeAuthRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !hubBearerTokenMatches(r, s.token) {
			writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "invalid hub authentication credentials")
			return
		}
		if hubDelegationOperatorRoute(r) {
			clone := r.Clone(r.Context())
			clone.Header = r.Header.Clone()
			clone.Header.Del("Authorization")
			r = clone
		}
		next.ServeHTTP(w, r)
	})
}

func hubNodeAuthRoute(r *http.Request) bool {
	if r.URL.Path == "/api/connect" {
		return true
	}
	return (r.URL.Path == "/api/delegations" || strings.HasPrefix(r.URL.Path, "/api/delegations/")) && strings.TrimSpace(r.Header.Get(hubNodeIDHeader)) != ""
}

func hubDelegationOperatorRoute(r *http.Request) bool {
	return (r.URL.Path == "/api/delegations" || strings.HasPrefix(r.URL.Path, "/api/delegations/")) && strings.TrimSpace(r.Header.Get(hubNodeIDHeader)) == ""
}

func hubBearerTokenMatches(r *http.Request, want string) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, rest, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	got := strings.TrimSpace(rest)
	if got == "" || strings.TrimSpace(want) == "" {
		return false
	}
	wantHash := sha256.Sum256([]byte(strings.TrimSpace(want)))
	gotHash := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wantHash[:], gotHash[:]) == 1
}

// hubBrowserRequestAllowed rejects cross-site browser requests before the hub
// exercises any token-injecting authority or mutates its node registry. This is
// defense-in-depth for --auth none and for bearer-authenticated browser use.
// Same-origin proxied node content is still trusted in v1; long-term host-based
// node isolation should remove that caveat. Requests without Origin and without
// Sec-Fetch-Site are allowed for non-browser clients; public hubs rely on bearer
// auth as the primary boundary for those requests.
func hubBrowserRequestAllowed(r *http.Request, requireJSON bool) bool {
	if requireJSON {
		ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if ct == "" || (!strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "application/merge-patch+json")) {
			return false
		}
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" || site == "same-site" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
