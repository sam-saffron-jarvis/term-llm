package usage

import (
	"context"
	"fmt"

	"github.com/term-llm/term-llm/internal/llm/providerusage"
)

func fetchConfiguredDirectProviderUsage(ctx context.Context, provider, apiKey string) (*providerusage.Report, error) {
	if provider == "chatgpt" {
		return providerusage.FetchWithAPIKey(ctx, provider, apiKey)
	}
	cfg, err := loadProviderUsageConfig()
	if err != nil {
		return nil, fmt.Errorf("load endpoint for %s usage: %w", provider, err)
	}
	base := ""
	if cfg != nil {
		base, err = cfg.ProviderBaseURL(provider, "", "")
		if err != nil {
			return nil, err
		}
	}
	return providerusage.FetchWithBaseURL(ctx, provider, apiKey, base)
}
