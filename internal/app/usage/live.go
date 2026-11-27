package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/providerusage"
)

// LiveReport fetches a provider's current subscription limits without starting
// a model turn. It loads configuration once and shares each provider's fetcher
// with `term-llm usage`, accepting canonical provider keys (case-insensitively)
// and configured custom provider aliases.
func LiveReport(ctx context.Context, provider string) (*providerusage.Report, error) {
	cfg, err := loadProviderUsageConfig()
	if err != nil {
		return nil, errors.New("could not load provider usage configuration")
	}
	return LiveReportWithConfig(ctx, cfg, provider)
}

// NewLiveFetcher returns a live usage fetcher backed by an immutable snapshot
// of cfg so callers such as chat avoid reloading config or re-running proxy
// discovery on every /usage invocation.
func NewLiveFetcher(cfg *config.Config) func(ctx context.Context, provider string) (*providerusage.Report, error) {
	snapshot := snapshotUsageConfig(cfg)
	return func(ctx context.Context, provider string) (*providerusage.Report, error) {
		return LiveReportWithConfig(ctx, snapshot, provider)
	}
}

// LiveReportWithConfig fetches a provider's current subscription limits using
// the provided configuration without reloading from disk or mutating cfg.
func LiveReportWithConfig(ctx context.Context, cfg *config.Config, provider string) (*providerusage.Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	providerKey, providerType := resolveLiveUsageProvider(cfg, provider)
	if cfg != nil {
		switch providerType {
		case config.ProviderTypeAgyBin, config.ProviderTypeClaudeBin, config.ProviderTypeCursorBin:
			// Direct quota fetchers use the default CLI account, not the
			// per-provider environment used by inference.
			if pc := cfg.GetProviderConfig(providerKey); pc != nil && strings.TrimSpace(pc.ProxyBinding) == "" && len(pc.Env) > 0 {
				return nil, fmt.Errorf("live usage for provider %q does not support per-provider CLI environment", providerKey)
			}
		}
	}
	switch providerType {
	case config.ProviderTypeAgyBin:
		var report *providerusage.Report
		err := withAgyBinUsageWithConfig(ctx, cfg, providerKey, func(_ json.RawMessage, data agyBinUsageReport, source string) (err error) {
			if report, err = normalizeAgyBinUsage(data); err == nil {
				report.Source = source
			}
			return err
		})
		return report, err
	case config.ProviderTypeClaudeBin:
		return claudeBinLiveReport(ctx, cfg, providerKey)
	case config.ProviderTypeCursorBin:
		_, report, err := loadCursorBinUsageWithConfig(ctx, cfg, providerKey)
		if err != nil {
			return nil, err
		}
		return cursorBinProviderReport(report), nil
	case config.ProviderTypeChatGPT, config.ProviderTypeGrok, config.ProviderTypeOpenCodeGo:
		apiKey, err := resolvedProviderUsageAPIKeyFromConfig(cfg, providerKey, providerType)
		if err != nil {
			return nil, err
		}
		return fetchDirectProviderUsageWithConfig(ctx, cfg, providerKey, providerType, apiKey)
	default:
		return nil, fmt.Errorf("live usage is not supported for provider %q", strings.TrimSpace(provider))
	}
}

func resolveLiveUsageProvider(cfg *config.Config, provider string) (string, config.ProviderType) {
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return "", ""
	}
	if cfg != nil {
		if pc := cfg.GetProviderConfig(trimmed); pc != nil {
			return trimmed, config.InferProviderType(trimmed, pc.Type)
		}
		if lower := strings.ToLower(trimmed); lower != trimmed {
			if pc := cfg.GetProviderConfig(lower); pc != nil {
				return lower, config.InferProviderType(lower, pc.Type)
			}
		}
	}
	lower := strings.ToLower(trimmed)
	return lower, config.InferProviderType(lower, "")
}

func snapshotUsageConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	snapshot := &config.Config{
		Proxy: config.ProxyConfig{
			URL: cfg.Proxy.URL,
		},
	}
	if len(cfg.Providers) > 0 {
		snapshot.Providers = make(map[string]config.ProviderConfig, len(cfg.Providers))
		for name, pc := range cfg.Providers {
			if pc.Env != nil {
				pc.Env = maps.Clone(pc.Env)
			}
			if pc.Headers != nil {
				pc.Headers = maps.Clone(pc.Headers)
			}
			snapshot.Providers[name] = pc
		}
	}
	return snapshot
}
