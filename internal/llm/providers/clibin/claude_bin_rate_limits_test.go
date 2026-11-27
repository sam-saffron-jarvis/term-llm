package clibin

import (
	"context"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// Shape observed from Claude Code 2.1.332 on a claude.ai subscription signed in
// with CLAUDE_CODE_OAUTH_TOKEN (no user:profile scope, so get_usage has no limits).
const claudeRateLimitEventLine = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1795630200,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.15,"resetsAt":1795630200,"status":"allowed"},"seven_day":{"utilization":0.09,"resetsAt":1795946400,"status":"allowed"}}},"uuid":"u","session_id":"s"}`

const claudeProxyRateLimitEventLine = `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.5,"resetsAt":1795630200}}}}`

// feedClaudeLine handles line as output of a Claude Code process launched with env.
func feedClaudeLine(t *testing.T, p *ClaudeBinProvider, env []string, line string) {
	t.Helper()
	feedClaudeLineWithContext(t, p, withClaudeAccount(context.Background(), p.claudeLaunchAccount(env)), line)
}

func feedClaudeLineWithContext(t *testing.T, p *ClaudeBinProvider, ctx context.Context, line string) {
	t.Helper()
	send := eventSender{ctx: ctx, ch: make(chan core.Event, 8)}
	var (
		lastUsage       *core.Usage
		sawTextDelta    bool
		fallbackText    string
		handledTerminal bool
	)
	if err := p.handleClaudeLine(ctx, line, false, send, &lastUsage, &sawTextDelta, &fallbackText, &handledTerminal, nil, false); err != nil {
		t.Fatalf("handleClaudeLine: %v", err)
	}
}

func resetObservedClaudeRateLimits(t *testing.T) {
	t.Helper()
	reset := func() {
		claudeRateLimits.mu.Lock()
		claudeRateLimits.byAccount = nil
		claudeRateLimits.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// tokenA is the launch environment of a direct setup-token login.
func tokenA() []string { return []string{"HOME=/home/user", "CLAUDE_CODE_OAUTH_TOKEN=token-a"} }

func proxiedClaudeProvider(root string) (*ClaudeBinProvider, []string) {
	p := NewClaudeBinProvider("", nil)
	p.extraEnv = map[string]string{claudeProxyRootEnv: root}
	return p, append(tokenA(), claudeProxyRootEnv+"="+root)
}

func TestClaudeRateLimitEventRecordsDirectLoginAndProxyRoutes(t *testing.T) {
	resetObservedClaudeRateLimits(t)
	const root = "https://proxy.example.test/claude-bin"

	before := time.Now()
	feedClaudeLine(t, NewClaudeBinProvider("", nil), tokenA(), claudeRateLimitEventLine)
	proxied, proxyEnv := proxiedClaudeProvider(root)
	feedClaudeLine(t, proxied, proxyEnv, claudeProxyRateLimitEventLine)

	got, ok := ObservedClaudeRateLimits("", tokenA())
	if !ok || len(got.Windows) != 2 || got.ObservedAt.Before(before) {
		t.Fatalf("direct reading = %+v ok=%v", got, ok)
	}
	if w := got.Windows["five_hour"]; w.Utilization != 0.15 || !w.ResetsAt.Equal(time.Unix(1795630200, 0)) {
		t.Fatalf("five_hour = %+v", w)
	}
	if w := got.Windows["seven_day"]; w.Utilization != 0.09 || !w.ResetsAt.Equal(time.Unix(1795946400, 0)) {
		t.Fatalf("seven_day = %+v", w)
	}
	got, ok = ObservedClaudeRateLimits(root, nil)
	if !ok || len(got.Windows) != 1 || got.Windows["five_hour"].Utilization != 0.5 {
		t.Fatalf("proxied reading = %+v ok=%v", got, ok)
	}
}

func TestClaudeRateLimitEventReplacesTheWholeReading(t *testing.T) {
	resetObservedClaudeRateLimits(t)
	p := NewClaudeBinProvider("", nil)
	feedClaudeLine(t, p, tokenA(), claudeRateLimitEventLine)
	// A later single-window event must not keep the older weekly value under its timestamp.
	feedClaudeLine(t, p, tokenA(), `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","utilization":0.2,"resetsAt":1795630300}}`)
	got, ok := ObservedClaudeRateLimits("", tokenA())
	if !ok || len(got.Windows) != 1 || got.Windows["five_hour"].Utilization != 0.2 || !got.Windows["five_hour"].ResetsAt.Equal(time.Unix(1795630300, 0)) {
		t.Fatalf("reading = %+v ok=%v", got, ok)
	}
	// A newer event without utilization leaves nothing current to show.
	feedClaudeLine(t, p, tokenA(), `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","resetsAt":1795630300}}`)
	if got, ok := ObservedClaudeRateLimits("", tokenA()); ok {
		t.Fatalf("kept a stale reading: %+v", got)
	}
}

func TestClaudeRateLimitEventMatchesEquivalentProxyRoots(t *testing.T) {
	resetObservedClaudeRateLimits(t)
	first, firstEnv := proxiedClaudeProvider("https://proxy.example.test/claude-bin")
	feedClaudeLine(t, first, firstEnv, claudeRateLimitEventLine)
	second, secondEnv := proxiedClaudeProvider("https://proxy.example.test:443/claude-bin")
	feedClaudeLine(t, second, secondEnv, claudeProxyRateLimitEventLine)
	for _, root := range []string{"https://proxy.example.test/claude-bin", "https://proxy.example.test:443/claude-bin"} {
		if got, ok := ObservedClaudeRateLimits(root, nil); !ok || len(got.Windows) != 1 || got.Windows["five_hour"].Utilization != 0.5 {
			t.Fatalf("%s: reading = %+v ok=%v", root, got, ok)
		}
	}
	if got, ok := ObservedClaudeRateLimits("https://proxy.example.test/other", nil); ok {
		t.Fatalf("matched another route: %+v", got)
	}
}

func TestClaudeRateLimitEventIsBoundToTheLaunchedLogin(t *testing.T) {
	resetObservedClaudeRateLimits(t)
	assertNone := func(name string) {
		t.Helper()
		if got, ok := ObservedClaudeRateLimits("", tokenA()); ok {
			t.Fatalf("%s: kept a reading: %+v", name, got)
		}
	}

	feedClaudeLine(t, NewClaudeBinProvider("", nil), []string{"HOME=/home/user"}, claudeRateLimitEventLine)
	assertNone("no token")
	feedClaudeLine(t, NewClaudeBinProvider("", nil), append([]string{"ANTHROPIC_API_KEY=sk"}, tokenA()...), claudeRateLimitEventLine)
	assertNone("API key")
	// Provider env can select another account in ways term-llm cannot enumerate
	// (including case-insensitive names on Windows), so it disables direct readings.
	for _, key := range []string{"claude_code_oauth_token", "CLAUDE_CODE_API_BASE_URL", "HTTPS_PROXY"} {
		p := NewClaudeBinProvider("", nil)
		p.extraEnv = map[string]string{key: "provider-specific"}
		feedClaudeLine(t, p, tokenA(), claudeRateLimitEventLine)
		assertNone("provider env " + key)
	}
	// Lines handled outside a launched command carry no account.
	feedClaudeLineWithContext(t, NewClaudeBinProvider("", nil), context.Background(), claudeRateLimitEventLine)
	assertNone("no launch account")

	// The identity comes from the launched process, not the environment when the event or lookup happens.
	feedClaudeLine(t, NewClaudeBinProvider("", nil), tokenA(), claudeRateLimitEventLine)
	if _, ok := ObservedClaudeRateLimits("", tokenA()); !ok {
		t.Fatal("missing reading for the launched login")
	}
	// Simple mode disables OAuth for get_usage, so its token says nothing about the queried login.
	if got, ok := ObservedClaudeRateLimits("", append([]string{"CLAUDE_CODE_SIMPLE=1"}, tokenA()...)); ok {
		t.Fatalf("matched a get_usage that cannot use the OAuth login: %+v", got)
	}
	if got, ok := ObservedClaudeRateLimits("", []string{"CLAUDE_CODE_OAUTH_TOKEN=token-b"}); ok {
		t.Fatalf("returned another login's reading: %+v", got)
	}
}
