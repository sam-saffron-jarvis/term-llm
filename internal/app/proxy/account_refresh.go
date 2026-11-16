package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/term-llm/term-llm/internal/oauth"
)

// Claude refresh parameters are source-backed by installed Claude Code 2.1.325:
// embedded k_e builds JSON grant_type/refresh_token/client_id/scope, using dn's
// production TOKEN_URL/CLIENT_ID and B5t's scopes. No profile-fetch or inference
// retry is performed here. This is protocol support, not live vendor acceptance.
const claudeTokenURL = "https://platform.claude.com/v1/oauth/token"
const claudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

var claudeRefreshScopes = []string{"user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload", "user:plugins"}

func accountIssuer(a accountRecord) (string, string) {
	issuer, client := "", ""
	switch a.Kind {
	case "chatgpt":
		issuer, client = oauth.ChatGPTTokenEndpoint, oauth.ChatGPTClientID
	case "grok":
		issuer, client = oauth.GrokTokenEndpoint, oauth.GrokClientID
	case "copilot":
		issuer = "https://api.github.com/copilot_internal/v2/token"
	case "claude-code":
		issuer, client = claudeTokenURL, claudeClientID
	}
	if a.operator.Issuer != "" {
		issuer = a.operator.Issuer
	}
	if a.operator.ClientID != "" {
		client = a.operator.ClientID
	}
	return issuer, client
}

// ensureAccount is a per-account flight with a generation-aware rejection rule.
// rejected==0 is proactive; otherwise only the rejected generation can refresh.
// A durable refreshing fence precedes consuming any refresh token. A crash,
// timeout or failed result commit therefore cannot cause automatic token reuse.
func (s *Service) ensureAccount(ctx context.Context, name string, rejected int64) (accountRecord, error) {
	unlock, lockErr := s.accountLock(ctx, name)
	if lockErr != nil {
		return accountRecord{}, errAccountUnavailable
	}
	defer unlock()
	// Once admitted to this account's flight, custody belongs to the proxy, not
	// the worker's connection. A disconnect must not strand a consumed rotation.
	// Keep this handler-owned operation bounded; the inference context is unchanged.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	a, err := s.store.account(name)
	if err != nil {
		return a, errAccountUnavailable
	}
	if tokenOnlyAccount(a) {
		return s.ensureTokenOnlyAccount(ctx, a, rejected)
	}
	if a.Kind == "claude-code" && a.State == "ready" && validateAccountCredentials(a.Kind, a.credentials) != nil {
		return a, errAccountUnavailable
	}
	if a.Kind == "copilot" && a.State == "refreshing" {
		// A failed state-restoration commit may leave this nonrotating fence behind.
		// Recover through a durable cooldown, not through a fresh user login.
		s.retryAccount(a)
		if fallback, ok := s.fallbackAccount(a, rejected); ok {
			return fallback, nil
		}
		return a, errAccountRefreshLimited
	}
	if a.State != "ready" {
		return a, errAccountUnavailable
	}
	s.rememberAccount(a.Name, a.credentials)
	if rejected != 0 && a.Generation != rejected {
		if s.accountGenerationRejected(a) {
			return a, errAccountRefreshLimited
		}
		return a, nil
	}
	now := time.Now()
	if a.Kind == "aws-sigv4" {
		if a.ExpiresAt != 0 && a.ExpiresAt <= now.Unix() {
			s.failAccount(ctx, a)
			return a, errAccountUnavailable
		}
		// A vendor rejection is not evidence that IAM credentials were destroyed.
		// There is no refresh operation for explicitly supplied signing keys.
		if rejected != 0 {
			return a, errAccountRefreshLimited
		}
		return a, nil
	}
	if rejected != 0 {
		// Latch rejection before attempting a durable update. A storage failure must
		// not make this process fall back to a token it has already seen rejected.
		s.accountRejected.Store(name, a.Generation)
		if a.refresh.RejectedGeneration != a.Generation {
			a.refresh.RejectedGeneration = a.Generation
			if s.store.replaceAccount(ctx, a, a.Generation) != nil {
				// No refresh token has been consumed. The memory latch still prevents
				// reuse of this generation; a transient write failure is not sign-out.
				return a, errAccountRefreshLimited
			}
		}
	}
	if a.refresh.RetryAfter > now.UnixMilli() {
		if rejected == 0 && s.usableAccountAccess(a, now) {
			return a, nil
		}
		return a, errAccountRefreshLimited
	}
	if rejected == 0 && !s.accountGenerationRejected(a) && a.credentials.AccessToken != "" && a.ExpiresAt > now.Add(5*time.Minute).Unix() {
		return a, nil
	}
	if a.refresh.LastSuccess > now.Add(-accountRefreshInterval).UnixMilli() {
		if rejected == 0 && s.usableAccountAccess(a, now) {
			return a, nil
		}
		return a, errAccountRefreshLimited
	}
	if err = s.store.fenceAccount(ctx, name, a.Generation); err != nil {
		return a, errAccountUnavailable
	}
	next, err := s.exchangeAccount(ctx, a)
	if err != nil {
		var failure *accountExchangeFailure
		if a.Kind == "copilot" || errors.As(err, &failure) && failure.safeBeforeConnection {
			s.retryAccount(a)
			if fallback, ok := s.fallbackAccount(a, rejected); ok {
				return fallback, nil
			}
		} else {
			s.failAccount(context.Background(), a)
		}
		return a, errAccountUnavailable
	}
	next.Generation = a.Generation + 1
	next.State = "ready"
	next.Updated = time.Now().UnixMilli()
	next.refresh = accountRefreshState{LastSuccess: next.Updated}
	if err = s.store.replaceAccount(ctx, next, a.Generation); err != nil {
		// Copilot exchanges do not consume the long-lived GitHub credential.
		if a.Kind == "copilot" {
			s.retryAccount(a)
		} else {
			s.failAccount(context.Background(), a)
		}
		return a, errAccountUnavailable
	}
	s.rememberAccount(next.Name, next.credentials)
	s.accountRejected.Delete(name)
	return next, nil
}
func (s *Service) failAccount(ctx context.Context, a accountRecord) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	a.State = "needs_login"
	a.Updated = time.Now().UnixMilli()
	_ = s.store.replaceAccount(ctx, a, a.Generation)
	s.mu.Lock()
	s.cancelAccountSockets(a.Name)
	s.mu.Unlock()
}

type accountTokenReply struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Token        string `json:"token"`
	ExpiresAt    int64  `json:"expires_at"`
	Scope        string `json:"scope"`
	Endpoints    struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

func (s *Service) exchangeAccount(ctx context.Context, a accountRecord) (accountRecord, error) {
	if tokenOnlyAccount(a) {
		return a, errAccountUnavailable
	}
	issuer, clientID := accountIssuer(a)
	var body io.Reader
	method := http.MethodPost
	contentType := "application/x-www-form-urlencoded"
	if a.Kind == "copilot" {
		method = http.MethodGet
	} else if a.Kind == "claude-code" {
		scopes := a.credentials.Scopes
		if len(scopes) == 0 {
			scopes = claudeRefreshScopes
		}
		data, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": a.credentials.RefreshToken, "client_id": clientID, "scope": strings.Join(scopes, " ")})
		body = strings.NewReader(string(data))
		contentType = "application/json"
	} else {
		body = strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {a.credentials.RefreshToken}, "client_id": {clientID}}.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, issuer, body)
	if err != nil {
		return a, errAccountUnavailable
	}
	req.GetBody = nil
	if req.Body == nil || req.Body == http.NoBody {
		req.Body = io.NopCloser(strings.NewReader(""))
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if a.Kind == "copilot" {
		req.Header.Set("Authorization", "token "+a.credentials.GitHubToken)
		setCopilotHeaders(req.Header)
	}
	// Use a separate pool: the inference 401 body still owns its connection.
	// A same-origin fixture (or a saturated vendor pool) must not queue the issuer
	// behind that body while we preserve the original 401 bytes for the caller.
	issuerTransport := s.transport.Clone()
	issuerTransport.MaxConnsPerHost = 1
	defer issuerTransport.CloseIdleConnections()
	// Fixed endpoints, no environment proxy, no redirects, bounded body/deadline.
	client := &http.Client{Transport: issuerTransport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var connected atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}))
	resp, err := client.Do(req)
	if err != nil {
		return a, &accountExchangeFailure{safeBeforeConnection: !connected.Load()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (128<<10)+1))
	if err != nil || len(data) > 128<<10 || resp.StatusCode != 200 {
		return a, errAccountUnavailable
	}
	var reply accountTokenReply
	if json.Unmarshal(data, &reply) != nil {
		return a, errAccountUnavailable
	}
	if a.Kind == "copilot" {
		a.credentials.AccessToken = reply.Token
		a.ExpiresAt = reply.ExpiresAt
		if reply.Endpoints.API != "" {
			origin, e := Origin(reply.Endpoints.API)
			if e != nil || !accountOriginAllowed(a, origin) {
				return a, errAccountUnavailable
			}
			a.endpoint = origin
		}
	} else {
		a.credentials.AccessToken = reply.AccessToken
		if reply.RefreshToken != "" {
			a.credentials.RefreshToken = reply.RefreshToken
		}
		if reply.ExpiresIn <= 0 || reply.ExpiresIn > 365*24*3600 {
			return a, errAccountUnavailable
		}
		a.ExpiresAt = time.Now().Unix() + reply.ExpiresIn
		if a.Kind == "claude-code" && reply.Scope != "" {
			a.credentials.Scopes = strings.Fields(reply.Scope)
		}
	}
	a.credentials.ExpiresAt = a.ExpiresAt
	if a.credentials.AccessToken == "" || a.ExpiresAt <= time.Now().Add(time.Minute).Unix() || validateAccountCredentials(a.Kind, a.credentials) != nil {
		return a, errAccountUnavailable
	}
	return a, nil
}
func setCopilotHeaders(h http.Header) {
	h.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	h.Set("Editor-Version", "vscode/1.96.0")
	h.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	h.Set("X-Github-Api-Version", "2025-04-01")
}

const accountRefreshInterval = 2 * time.Minute
const accountRetryBase = 5 * time.Second
const accountRetryMaximum = 2 * time.Minute

var errAccountRefreshLimited = errors.New("account refresh temporarily limited")

type accountRefreshState struct {
	LastSuccess        int64 `json:"last_success,omitempty"`
	RetryAfter         int64 `json:"retry_after,omitempty"`
	Failures           int   `json:"failures,omitempty"`
	RejectedGeneration int64 `json:"rejected_generation,omitempty"`
}
type accountExchangeFailure struct{ safeBeforeConnection bool }

func (*accountExchangeFailure) Error() string { return "account issuer unavailable" }

// Only retry a rotating credential when Transport never acquired a connection.
// Once GotConn fires, any write failure, timeout or issuer response is uncertain.
// Copilot's GitHub credential is nonrotating, so every exchange failure can
// safely retry with the same bounded, persisted backoff and no inference replay.
func (s *Service) retryAccount(a accountRecord) {
	if a.refresh.Failures < 6 {
		a.refresh.Failures++
	}
	delay := accountRetryBase * time.Duration(1<<uint(a.refresh.Failures-1))
	if delay > accountRetryMaximum {
		delay = accountRetryMaximum
	}
	a.refresh.RetryAfter = time.Now().Add(delay).UnixMilli()
	a.State = "ready"
	a.Updated = time.Now().UnixMilli()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.store.replaceAccount(ctx, a, a.Generation) != nil && a.Kind != "copilot" {
		s.failAccount(context.Background(), a)
	}
}

func (s *Service) accountGenerationRejected(a accountRecord) bool {
	if a.refresh.RejectedGeneration == a.Generation {
		return true
	}
	generation, ok := s.accountRejected.Load(a.Name)
	return ok && generation.(int64) == a.Generation
}
func (s *Service) usableAccountAccess(a accountRecord, now time.Time) bool {
	return a.State == "ready" && a.credentials.AccessToken != "" && a.ExpiresAt > now.Unix() && !s.accountGenerationRejected(a)
}

// Fallback is proactive-only, and only after rereading the durable restoration
// of the same generation. Never use uncertain rotated results or rejected tokens.
func (s *Service) fallbackAccount(a accountRecord, rejected int64) (accountRecord, bool) {
	if rejected != 0 {
		return accountRecord{}, false
	}
	current, err := s.store.account(a.Name)
	return current, err == nil && current.Generation == a.Generation && s.usableAccountAccess(current, time.Now())
}

// Token-only access has no renewal protocol. Zero expiry is deliberately unknown
// and must never be replaced with an invented lifetime or an unverified JWT claim.
func (s *Service) ensureTokenOnlyAccount(ctx context.Context, a accountRecord, rejected int64) (accountRecord, error) {
	if validateAccountCredentials(a.Kind, a.credentials) != nil {
		return a, errAccountUnavailable
	}
	expired := a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().Unix()
	knownRejected := s.accountGenerationRejected(a)
	currentRejection := rejected != 0 && rejected == a.Generation
	if expired || knownRejected || currentRejection {
		s.invalidateTokenOnlyAccount(ctx, a)
		return a, errAccountUnavailable
	}
	if a.State != "ready" {
		return a, errAccountUnavailable
	}
	// A late rejection from an explicitly replaced generation must not sign out
	// the replacement. This adopts it for future calls, never replays inference.
	return a, nil
}
func (s *Service) invalidateTokenOnlyAccount(ctx context.Context, a accountRecord) {
	s.accountRejected.Store(a.Name, a.Generation)
	a.refresh.RejectedGeneration = a.Generation
	a.State = "needs_login"
	a.Updated = time.Now().UnixMilli()
	if s.store.replaceAccount(ctx, a, a.Generation) != nil {
		// If the state-column change is unavailable, persist the rejection proof on
		// the existing generation instead. Readers derive needs_login from it too.
		// If storage refuses every write, the in-process latch still fails closed;
		// a later call retries persistence before it can dispatch anything.
		a.State = "ready"
		_ = s.store.replaceAccount(ctx, a, a.Generation)
	}
	s.mu.Lock()
	s.cancelAccountSockets(a.Name)
	s.mu.Unlock()
}
