package clibin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

// ClaudeRateLimitWindow is one plan usage window, e.g. "five_hour" or "seven_day".
type ClaudeRateLimitWindow struct {
	Utilization float64 // Fraction of the window already used, 0..1
	ResetsAt    time.Time
}

// ClaudeRateLimits is the plan reading from Claude Code's latest rate_limit_event.
type ClaudeRateLimits struct {
	ObservedAt time.Time
	Windows    map[string]ClaudeRateLimitWindow
}

type claudeAccountContextKey struct{}

// Plan limits belong to the account, not a conversation, so every claude-bin
// provider in the process shares the latest reading per account key: a
// canonical credential-proxy root, or a digest of a direct login's OAuth token.
var claudeRateLimits struct {
	mu        sync.Mutex
	byAccount map[string]ClaudeRateLimits
}

type claudeRateLimitWindowJSON struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    int64    `json:"resetsAt"`
}

type claudeRateLimitEvent struct {
	Info struct {
		RateLimitType string `json:"rateLimitType"`
		claudeRateLimitWindowJSON
		UnifiedWindows map[string]claudeRateLimitWindowJSON `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// claudeEnvValue reads the last value of name, case-insensitively on Windows.
func claudeEnvValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		key, value, ok := strings.Cut(env[i], "=")
		if ok && (key == name || runtime.GOOS == "windows" && strings.EqualFold(key, name)) {
			return value
		}
	}
	return ""
}

func proxyClaudeAccount(root string) string {
	canonical, err := proxyclient.CanonicalRoot(strings.TrimSpace(root))
	if err != nil {
		return ""
	}
	return "proxy:" + canonical
}

// directClaudeAccount identifies a direct login by its CLAUDE_CODE_OAUTH_TOKEN,
// the setup-token login whose get_usage lacks plan limits. Only a digest is kept;
// other logins, or a process that cannot use OAuth (API key, simple mode),
// cannot be matched reliably, so they have no account.
func directClaudeAccount(env []string) string {
	token := strings.TrimSpace(claudeEnvValue(env, "CLAUDE_CODE_OAUTH_TOKEN"))
	if token == "" || claudeEnvValue(env, "ANTHROPIC_API_KEY") != "" || claudeEnvValue(env, "CLAUDE_CODE_SIMPLE") != "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "direct:" + hex.EncodeToString(sum[:8])
}

// claudeLaunchAccount identifies whose plan a Claude Code process launched with
// env reports on, or "" when its readings cannot be attributed.
func (p *ClaudeBinProvider) claudeLaunchAccount(env []string) string {
	// The same case-sensitive lookup as the launcher, which validates this route.
	if root := envValue(env, claudeProxyRootEnv); strings.TrimSpace(root) != "" {
		return proxyClaudeAccount(root)
	}
	// Provider env can select another login in ways that cannot be enumerated.
	if len(p.extraEnv) > 0 {
		return ""
	}
	return directClaudeAccount(env)
}

// withClaudeAccount tags a turn so its rate_limit_event readings can be attributed.
func withClaudeAccount(ctx context.Context, account string) context.Context {
	if account == "" {
		return ctx
	}
	return context.WithValue(ctx, claudeAccountContextKey{}, account)
}

// recordClaudeRateLimits keeps the plan windows Claude Code reports with each
// API response, even when its sign-in lacks the user:profile scope that
// get_usage requires. Each event replaces the previous reading for its account.
func recordClaudeRateLimits(ctx context.Context, line string) {
	account, _ := ctx.Value(claudeAccountContextKey{}).(string)
	var event claudeRateLimitEvent
	if account == "" || json.Unmarshal([]byte(line), &event) != nil {
		return
	}
	windows := event.Info.UnifiedWindows
	if len(windows) == 0 && event.Info.RateLimitType != "" {
		windows = map[string]claudeRateLimitWindowJSON{event.Info.RateLimitType: event.Info.claudeRateLimitWindowJSON}
	}
	limits := ClaudeRateLimits{ObservedAt: time.Now(), Windows: make(map[string]ClaudeRateLimitWindow, len(windows))}
	for name, window := range windows {
		if window.Utilization != nil && window.ResetsAt > 0 {
			limits.Windows[name] = ClaudeRateLimitWindow{Utilization: *window.Utilization, ResetsAt: time.Unix(window.ResetsAt, 0)}
		}
	}

	claudeRateLimits.mu.Lock()
	defer claudeRateLimits.mu.Unlock()
	if len(limits.Windows) == 0 {
		delete(claudeRateLimits.byAccount, account)
		return
	}
	if claudeRateLimits.byAccount == nil {
		claudeRateLimits.byAccount = make(map[string]ClaudeRateLimits)
	}
	claudeRateLimits.byAccount[account] = limits
}

// ObservedClaudeRateLimits returns the latest plan reading Claude Code reported
// in this process for a credential-proxy root or, when proxyRoot is "", for the
// direct login a Claude Code process started with env would use.
func ObservedClaudeRateLimits(proxyRoot string, env []string) (ClaudeRateLimits, bool) {
	account := directClaudeAccount(env)
	if strings.TrimSpace(proxyRoot) != "" {
		account = proxyClaudeAccount(proxyRoot)
	}
	if account == "" {
		return ClaudeRateLimits{}, false
	}
	claudeRateLimits.mu.Lock()
	defer claudeRateLimits.mu.Unlock()
	limits, ok := claudeRateLimits.byAccount[account]
	limits.Windows = maps.Clone(limits.Windows)
	return limits, ok
}
