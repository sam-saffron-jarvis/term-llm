package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

// AccountRef is operator-file-only, read once for a previously unknown name.
// Remove the source login after importing rotating credentials: this is a move,
// not a safe way to share a single-use refresh token between two installations.
type AccountRef struct {
	Kind string `json:"kind" yaml:"kind"`
	File string `json:"file" yaml:"file"`
}

type accountCredentials struct {
	Mode            string   `json:"mode,omitempty"`
	AccessToken     string   `json:"access_token,omitempty"`
	RefreshToken    string   `json:"refresh_token,omitempty"`
	GitHubToken     string   `json:"github_token,omitempty"`
	AccountID       string   `json:"account_id,omitempty"`
	ExpiresAt       int64    `json:"expires_at,omitempty"`
	AccessKeyID     string   `json:"access_key_id,omitempty"`
	SecretAccessKey string   `json:"secret_access_key,omitempty"`
	SessionToken    string   `json:"session_token,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

// Only bootstrap files may supply these. Admin JSON has no such fields.
type accountOperator struct {
	Issuer   string   `json:"issuer,omitempty"`
	Origins  []string `json:"origins,omitempty"`
	ClientID string   `json:"client_id,omitempty"`
	Region   string   `json:"region,omitempty"`
}
type accountBootstrap struct {
	accountCredentials
	accountOperator
}

var errAccountUnavailable = errors.New("trusted account unavailable; sign in or check account configuration")

func accountKind(kind string) bool {
	switch kind {
	case "chatgpt", "grok", "copilot", "claude-code", "aws-sigv4":
		return true
	}
	return false
}
func validateAccountCredentials(kind string, c accountCredentials) error {
	switch c.Mode {
	case "": // Existing stored/imported credentials retain their original semantics.
	case "token_only", "rotating":
		if kind != "claude-code" {
			return inputError("credential mode is only supported for Claude Code accounts")
		}
	default:
		return inputError("credential mode must be token_only or rotating")
	}
	for _, v := range accountValues(c) {
		if len(v) > 16<<10 || !httpguts.ValidHeaderFieldValue(v) {
			return inputError("invalid account credential")
		}
	}
	// Native credential JSON uses Unix seconds, unlike browser timestamps.
	// Reject common millisecond pastes rather than treating a token as valid for millennia.
	if c.ExpiresAt < 0 || c.ExpiresAt > 253402300799 {
		return inputError("account expires_at must be Unix seconds (not milliseconds)")
	}
	switch kind {
	case "chatgpt", "grok":
		if c.AccessToken == "" || c.RefreshToken == "" || c.AccountID == "" || c.ExpiresAt == 0 {
			return inputError("access_token, refresh_token, account_id and expires_at required")
		}
	case "copilot":
		if c.GitHubToken == "" {
			return inputError("github_token required for Copilot session exchange")
		}
	case "claude-code":
		if c.Mode == "token_only" {
			if c.AccessToken == "" || strings.ContainsAny(c.AccessToken, " \t\r\n") || strings.HasPrefix(c.AccessToken, "sk-ant-api") || strings.HasPrefix(c.AccessToken, "sk-tlp-") || strings.HasPrefix(c.AccessToken, "admin_") {
				return inputError("token-only accounts require one bare subscription access_token, not an API key or proxy/admin token")
			}
			if c.RefreshToken != "" || c.GitHubToken != "" || c.AccessKeyID != "" || c.SecretAccessKey != "" || c.SessionToken != "" {
				return inputError("token-only accounts forbid refresh or other provider credentials")
			}
		} else if c.AccessToken == "" || c.RefreshToken == "" || c.ExpiresAt == 0 {
			return inputError("rotating accounts require access_token, refresh_token and expires_at")
		}
	case "aws-sigv4":
		if c.SessionToken != "" && c.ExpiresAt == 0 {
			return inputError("AWS temporary credentials require expires_at")
		}
		if c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return inputError("AWS access_key_id and secret_access_key required")
		}
	default:
		return inputError("unsupported account kind")
	}
	if len(c.Scopes) > 32 {
		return inputError("too many OAuth scopes")
	}
	for _, scope := range c.Scopes {
		if scope == "" || len(scope) > 128 || strings.ContainsAny(scope, " \r\n\t") {
			return inputError("invalid OAuth scope")
		}
	}
	return nil
}
func validateAccountOperator(o accountOperator) error {
	if o.Issuer != "" {
		u, e := url.Parse(o.Issuer)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !safePath(u.EscapedPath()) {
			return inputError("issuer override must be an HTTPS endpoint")
		}
	}
	if len(o.Origins) > 8 {
		return inputError("at most eight fixture origins")
	}
	for i, raw := range o.Origins {
		origin, e := secretOrigin(raw)
		if e != nil || !strings.HasPrefix(origin, "https://") {
			return inputError("fixture origins must be HTTPS origins")
		}
		o.Origins[i] = origin
	}
	if o.ClientID != "" && (len(o.ClientID) > 256 || !httpguts.ValidHeaderFieldValue(o.ClientID)) {
		return inputError("invalid client ID")
	}
	return nil
}
func (s *Service) accountLock(ctx context.Context, name string) (func(), error) {
	v, _ := s.accountLocks.LoadOrStore(name, make(chan struct{}, 1))
	gate := v.(chan struct{})
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) bootstrapAccounts(ctx context.Context, c Config) error {
	if len(c.Accounts) > 128 {
		return inputError("at most 128 account references")
	}
	if len(c.Accounts) > 0 && s.opts.ConfigFile == "" {
		return inputError("account bootstrap files are operator-only")
	}
	for name, ref := range c.Accounts {
		if !slug.MatchString(name) || !accountKind(ref.Kind) || ref.File == "" {
			return inputError("invalid operator account reference")
		}
		old, err := s.store.account(name)
		if err == nil {
			if old.Kind != ref.Kind {
				return inputError("existing account kind differs from bootstrap")
			}
			s.rememberAccount(old.Name, old.credentials)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return errAccountUnavailable
		}
		info, e := os.Stat(ref.File)
		if e != nil || forbiddenSecretFile(ref.File, info, []string{s.opts.StateDir, s.opts.AdminTokenFile, s.opts.PasskeyBootstrapTokenFile, s.opts.PasskeyRecoveryTokenFile}) {
			return inputError("account bootstrap file unavailable or forbidden")
		}
		data, e := privateFile(ref.File, 128<<10)
		if e != nil {
			return e
		}
		var b accountBootstrap
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if dec.Decode(&b) != nil || dec.Decode(new(any)) != io.EOF {
			return inputError("invalid private account bootstrap JSON")
		}
		if e = validateAccountCredentials(ref.Kind, b.accountCredentials); e != nil {
			return e
		}
		if e = validateAccountOperator(b.accountOperator); e != nil {
			return e
		}
		a := accountRecord{AccountInfo: AccountInfo{Name: name, Kind: ref.Kind, State: "ready", Generation: 1, ExpiresAt: b.ExpiresAt, Updated: time.Now().UnixMilli()}, credentials: b.accountCredentials, operator: b.accountOperator}
		if e = s.store.replaceAccount(ctx, a, 0); e != nil {
			return fmt.Errorf("commit imported account: %w", errAccountUnavailable)
		}
		s.rememberAccount(a.Name, a.credentials)
	}
	return nil
}
func (s *Service) validateAccounts(snap *snapshot) error {
	for _, r := range snap.routes {
		if r.Account == "" {
			continue
		}
		a, e := s.store.account(r.Account)
		if e != nil {
			return inputError("route references an unavailable account")
		}
		if !accountOriginAllowed(a, r.origin) {
			return inputError("account is not bound to this upstream origin")
		}
		for key := range r.SetHeaders {
			if accountControlledHeader(key) {
				return inputError("account-controlled headers cannot have static substitutions")
			}
		}
	}
	return nil
}
func accountValues(c accountCredentials) []string {
	return []string{c.AccessToken, c.RefreshToken, c.GitHubToken, c.AccountID, c.AccessKeyID, c.SecretAccessKey, c.SessionToken}
}

// Keep bounded recent material per account, plus exact in-flight generations.
func (s *Service) rememberAccount(name string, c accountCredentials) {
	values := accountValues(c)
	if old, ok := s.accountRedactions.Load(name); ok {
		values = append(values, old.([]string)...)
	}
	out := make([]string, 0, 32)
	for _, v := range values {
		if v == "" {
			continue
		}
		found := false
		for _, old := range out {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			out = append(out, v)
		}
		if len(out) == 32 {
			break
		}
	}
	s.accountRedactions.Store(name, out)
}

// redactAccountMaterial also accepts exact request-generation values so rotation
// never makes an in-flight request's credentials disappear from its redactor.
func (s *Service) redactAccountMaterial(text string, values ...string) string {
	s.accountRedactions.Range(func(_, v any) bool { values = append(values, v.([]string)...); return true })
	// Long values first prevent overlapping credentials from exposing suffixes.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		if v != "" {
			text = strings.ReplaceAll(text, v, "[redacted]")
		}
	}
	return text
}

func accountOriginAllowed(a accountRecord, origin string) bool {
	if len(a.operator.Origins) > 0 {
		for _, o := range a.operator.Origins {
			if o == origin {
				return true
			}
		}
		return false
	}
	var hosts []string
	switch a.Kind {
	case "chatgpt":
		hosts = []string{"https://chatgpt.com:443", "https://api.openai.com:443"}
	case "grok":
		hosts = []string{"https://cli-chat-proxy.grok.com:443"}
	case "copilot":
		hosts = []string{"https://api.githubcopilot.com:443", "https://api.business.githubcopilot.com:443", "https://api.enterprise.githubcopilot.com:443"}
	case "claude-code":
		hosts = []string{"https://api.anthropic.com:443"}
	case "aws-sigv4":
		if !validAWSRegion(a.operator.Region) {
			return false
		}
		hosts = []string{"https://bedrock-runtime." + a.operator.Region + ".amazonaws.com:443"}
		if strings.HasPrefix(a.operator.Region, "cn-") {
			hosts = []string{"https://bedrock-runtime." + a.operator.Region + ".amazonaws.com.cn:443"}
		}
	}
	for _, h := range hosts {
		if origin == h {
			return true
		}
	}
	return false
}
func validAWSRegion(region string) bool {
	if len(region) > 40 || len(region) < 5 {
		return false
	}
	for _, r := range region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// Prevent worker-selected alternate auth/identity fields from affecting custody.
func accountControlledHeader(h string) bool {
	switch strings.ToLower(h) {
	case "authorization", "x-api-key", "x-goog-api-key", "chatgpt-account-id", "openai-organization", "openai-project", "x-grok-user-id", "x-userid", "x-amz-date", "x-amz-security-token", "x-amz-content-sha256", "x-amz-credential", "x-xai-token-auth":
		return true
	}
	return false
}

func validAccountHeaders(h http.Header) bool {
	counts := map[string]int{}
	for key, values := range h {
		if accountControlledHeader(key) {
			key = strings.ToLower(key)
			counts[key] += len(values)
			if counts[key] > 1 {
				return false
			}
		}
	}
	return true
}

// Attribution is display metadata, never an identity or a credential source.
// The request owner may impose a stricter app-cardinality policy separately.
func boundedAttribution(value string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r < 32 || r == 127 {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > 80 {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

// accountAuthMode is display metadata, never an inference about a token's claims
// or lifetime. Only explicit token_only opts out of renewable Claude custody.
func accountAuthMode(kind string, c accountCredentials) string {
	if kind != "claude-code" {
		return ""
	}
	if c.Mode == "token_only" {
		return "token_only"
	}
	if c.Mode == "rotating" || c.Mode == "" && c.RefreshToken != "" {
		return "rotating"
	}
	return ""
}
func tokenOnlyAccount(a accountRecord) bool {
	return a.Kind == "claude-code" && a.credentials.Mode == "token_only"
}
