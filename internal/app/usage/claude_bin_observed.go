package usage

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/providers/clibin"
	"github.com/term-llm/term-llm/internal/llm/providerusage"
)

var observedClaudeRateLimits = clibin.ObservedClaudeRateLimits

// claudeBinLiveReport prefers get_usage. When it has no plan limits, as for a
// sign-in without the user:profile scope, it falls back to the windows Claude
// Code reported with its latest response for the same account in this process.
func claudeBinLiveReport(ctx context.Context, cfg *config.Config, providerKey string) (*providerusage.Report, error) {
	raw, account, err := loadClaudeBinUsageWithConfig(ctx, cfg, providerKey)
	if err != nil {
		return nil, err
	}
	usage, err := decodeClaudeBinUsage(raw)
	if err != nil {
		return nil, err
	}
	report, err := claudeBinProviderReport(usage, claudeBinUsageSource(account.proxyRoot))
	if errors.Is(err, errClaudeBinUsageUnavailable) || errors.Is(err, errClaudeBinNoUsageWindows) {
		if observed := claudeBinObservedReport(usage, account, time.Now()); observed != nil {
			return observed, nil
		}
	}
	return report, err
}

// claudeBinObservedReport fills get_usage's missing windows from the latest
// reading, keeping its other fields such as the plan.
func claudeBinObservedReport(usage claudeBinUsageReport, account claudeBinUsageAccount, now time.Time) *providerusage.Report {
	observed, ok := observedClaudeRateLimits(account.proxyRoot, account.env)
	if !ok {
		return nil
	}
	window := func(name string) *claudeBinRateLimit {
		w, ok := observed.Windows[name]
		// A window that has reset since the reading no longer describes the plan.
		if !ok || !w.ResetsAt.After(now) {
			return nil
		}
		utilization := math.Round(w.Utilization*1000) / 10
		resetsAt := w.ResetsAt.UTC().Format(time.RFC3339)
		return &claudeBinRateLimit{Utilization: &utilization, ResetsAt: &resetsAt}
	}
	usage.RateLimitsAvailable = true
	usage.RateLimits = &claudeBinRateLimits{
		FiveHour:          window("five_hour"),
		SevenDay:          window("seven_day"),
		SevenDayOAuthApps: window("seven_day_oauth_apps"),
		SevenDayOpus:      window("seven_day_opus"),
		SevenDaySonnet:    window("seven_day_sonnet"),
	}
	source := "Last Claude Code response"
	if account.proxyRoot != "" {
		source += " via credential proxy"
	}
	age := "less than a minute ago"
	if duration := providerusage.FormatDuration(now.Sub(observed.ObservedAt)); duration != "" {
		age = duration + " ago"
	}
	report, err := claudeBinProviderReport(usage, source+" · "+age)
	if err != nil {
		return nil
	}
	return report
}
