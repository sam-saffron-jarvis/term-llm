package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/awsauth"
)

const maxAccountSigningBody = 16 << 20

type accountTransport struct {
	service         *Service
	base            http.RoundTripper
	route           activeRoute
	used            []string
	dispatched      bool
	identityVersion int64
}

func (t *accountTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := t.service
	a, err := s.store.account(t.route.Account)
	if err != nil {
		return nil, errAccountUnavailable
	}
	origin, e := Origin(req.URL.Scheme + "://" + req.URL.Host)
	if e != nil || origin != t.route.origin || !accountOriginAllowed(a, origin) || !accountRequestAllowed(a, req, t.route) {
		return nil, errAccountUnavailable
	}
	a, err = s.ensureAccount(req.Context(), t.route.Account, 0)
	if err != nil {
		return nil, errAccountUnavailable
	}
	// Trusted rotation may finish after its triggering worker has disconnected.
	// Do not dispatch that canceled inference, or replace its original context.
	if err = req.Context().Err(); err != nil {
		return nil, err
	}
	origin, e = Origin(req.URL.Scheme + "://" + req.URL.Host)
	if e != nil || origin != t.route.origin || !accountOriginAllowed(a, origin) || !accountRequestAllowed(a, req, t.route) {
		return nil, errAccountUnavailable
	}
	if a.endpoint != "" && a.endpoint != origin {
		return nil, errAccountUnavailable
	}
	t.used = accountValues(a.credentials)
	t.identityVersion = a.identityVersion
	trailers := req.Trailer
	req = req.Clone(req.Context())
	req.Trailer = trailers
	for key := range req.Header {
		if accountControlledHeader(key) {
			delete(req.Header, key)
		}
	}
	// Inbound trailers cannot reintroduce a worker's credential or identity.
	for key := range req.Trailer {
		if accountControlledHeader(key) {
			delete(req.Trailer, key)
		}
	}
	c := a.credentials
	if a.Kind == "aws-sigv4" {
		if err = t.signAWS(req, a); err != nil {
			return nil, err
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
		headers := []string{}
		if a.Kind == "chatgpt" {
			headers = []string{"ChatGPT-Account-ID"}
		}
		if a.Kind == "grok" {
			headers = []string{"X-Grok-User-ID", "X-Userid"}
		}
		for _, h := range headers {
			if c.AccountID != "" {
				req.Header.Set(h, c.AccountID)
			}
		}
		if a.Kind == "grok" {
			req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
		}
		if a.Kind == "copilot" {
			setCopilotHeaders(req.Header)
			req.Header.Set("Copilot-Integration-Id", "vscode-chat")
			req.Header.Set("Openai-Intent", "conversation-panel")
		}
		if a.Kind == "claude-code" && !headerHasToken(req.Header, "Anthropic-Beta", "oauth-2025-04-20") {
			beta := req.Header.Get("Anthropic-Beta")
			if beta != "" {
				beta += ","
			}
			req.Header.Set("Anthropic-Beta", beta+"oauth-2025-04-20")
		}
	}
	t.dispatched = true // from here onward an inference charge cannot be ruled out
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Header.Del("X-Term-LLM-Proxy-Upstream-Auth")
	if resp.StatusCode == http.StatusUnauthorized {
		// A subscription may lack optional profile/usage scopes. Preserve that
		// original 401 without treating it as proof that inference auth expired.
		if tokenOnlyAccount(a) && (req.URL.Path == "/api/oauth/usage" || req.URL.Path == "/api/oauth/profile") {
			resp.Header.Set("X-Term-LLM-Proxy-Upstream-Auth", "scope-rejected")
			return resp, nil
		}
		_, e = s.ensureAccount(req.Context(), a.Name, a.Generation)
		if tokenOnlyAccount(a) {
			// Reconnect is an explicit owner action, never an automatic token refresh.
			resp.Header.Set("X-Term-LLM-Proxy-Upstream-Auth", "login-required")
		} else if e == nil {
			resp.Header.Set("X-Term-LLM-Proxy-Upstream-Auth", "refreshed")
		} else if errors.Is(e, errAccountRefreshLimited) {
			resp.Header.Set("X-Term-LLM-Proxy-Upstream-Auth", "refresh-limited")
		} else {
			resp.Header.Set("X-Term-LLM-Proxy-Upstream-Auth", "login-required")
		}
	}
	return resp, nil
}
func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == token {
				return true
			}
		}
	}
	return false
}
func (t *accountTransport) signAWS(req *http.Request, a accountRecord) error {
	limit := t.service.opts.MaxBody
	if limit > maxAccountSigningBody {
		limit = maxAccountSigningBody
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	req.Body.Close()
	if err != nil || int64(len(data)) > limit {
		return errAccountUnavailable
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.GetBody = nil
	// Do not restore GetBody: inference is never replayed, even after signing.
	sum := sha256.Sum256(data)
	c := a.credentials
	return awsauth.SignV4(req, hex.EncodeToString(sum[:]), awsauth.Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}, "bedrock", a.operator.Region, time.Now())
}

// Account custody has a vendor-fixed endpoint scope in addition to the route's
// caller allowlist. Fixture origins retain the explicit route scope but cannot
// be installed by an admin/import. Encoded account paths are never decoded twice.
func accountRequestAllowed(a accountRecord, req *http.Request, r activeRoute) bool {
	p := req.URL.EscapedPath()
	if a.Kind != "aws-sigv4" && !canonicalPolicyPath(p) {
		return false
	}
	base := strings.TrimSuffix(r.target.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(p, base) || !accountPolicyAllows(a, r.Route, req.Method, "/"+strings.TrimPrefix(p, base)) {
		return false
	}
	if len(a.operator.Origins) > 0 {
		return len(r.Allow) > 0
	}
	var rules []string
	switch a.Kind {
	case "chatgpt":
		rules = []string{"POST /backend-api/codex/responses", "GET /backend-api/codex/responses", "GET /backend-api/codex/models", "POST /backend-api/codex/images/generations", "POST /backend-api/codex/images/edits", "POST /backend-api/codex/realtime/calls", "POST /backend-api/transcribe", "GET /backend-api/wham/usage", "POST /v1/embeddings", "POST /v1/realtime/calls", "GET /v1/realtime"}
	case "grok":
		rules = []string{"POST /v1/responses", "POST /v1/chat/completions", "GET /v1/models", "GET /v1/billing"}
	case "copilot":
		rules = []string{"POST /responses", "POST /chat/completions", "GET /models"}
	case "claude-code":
		rules = []string{"POST /v1/messages", "POST /v1/messages/count_tokens", "GET /v1/models", "GET /api/oauth/usage", "GET /api/oauth/profile"}
	case "aws-sigv4":
		return req.Method == "POST" && validAWSModelPath(p)
	}
	for _, rule := range rules {
		method, prefix, _ := strings.Cut(rule, " ")
		if req.Method == method && (p == prefix || strings.HasPrefix(p, prefix+"/")) {
			return true
		}
	}
	return false
}

// AWS resource ARNs intentionally contain escaped ':' and '/' within the one
// model segment. Permit those only between a literal /model/ mount and a fixed
// operation suffix; never decode separators for route/policy prefix matching.
func validAWSModelPath(p string) bool {
	if !strings.HasPrefix(p, "/model/") || !safePath(p) {
		return false
	}
	tail := strings.TrimPrefix(p, "/model/")
	model, operation, ok := strings.Cut(tail, "/")
	if !ok || model == "" {
		return false
	}
	switch operation {
	case "invoke", "invoke-with-response-stream", "converse", "converse-stream":
	default:
		return false
	}
	decoded, err := url.PathUnescape(model)
	if err != nil {
		return false
	}
	for _, r := range decoded {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":./_-", r)) {
			return false
		}
	}
	return true
}
func accountPolicyAllows(a accountRecord, r Route, method, p string) bool {
	if a.Kind != "aws-sigv4" {
		return policyAllows(r, method, p)
	}
	if method != "POST" || !validAWSModelPath(p) {
		return false
	}
	if len(r.Allow) == 0 {
		return true
	}
	for _, rule := range r.Allow {
		m, prefix, _ := strings.Cut(rule, " ")
		if m == method && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
func (s *Service) routePolicyAllows(r activeRoute, method, p string) bool {
	if policyAllows(r.Route, method, p) {
		return true
	}
	if r.Account == "" {
		return false
	}
	a, err := s.store.account(r.Account)
	return err == nil && a.Kind == "aws-sigv4" && accountPolicyAllows(a, r.Route, method, p)
}
