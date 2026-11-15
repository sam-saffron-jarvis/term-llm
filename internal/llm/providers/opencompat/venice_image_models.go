package opencompat

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/venice"
)

const (
	veniceImageCacheKey          = venice.ImageCacheKey
	veniceImageCompletionTimeout = 2 * time.Second
)

// GetVeniceImageModelIDs returns live model suggestions filtered by API type:
// "image" for generation, "inpaint" for editing, or "" for both. Missing or
// stale catalogs are refreshed synchronously with a short timeout: background
// goroutines cannot reliably finish in a short-lived shell completion process.
// Deferred credentials are deliberately not executed by shell completion;
// cached and configured models remain available without unlocking a vault.
func GetVeniceImageModelIDs(cfg *config.Config, modelType string) []string {
	baseURL, custom, safe := veniceCompletionBaseURL(cfg)
	var cached *cache.ModelCache
	if !custom {
		cached, _ = cache.ReadModelCache(veniceImageCacheKey)
	}
	if safe && (!cache.IsCacheValid(cached) || len(cached.ModelInfos) == 0) {
		ref := config.Cred("", "VENICE_API_KEY")
		if cfg != nil {
			ref = cfg.Image.VeniceKey()
		}
		if !ref.IsDeferred() {
			if apiKey, err := ref.Resolve(); err == nil && apiKey != "" {
				ctx, cancel := context.WithTimeout(context.Background(), veniceImageCompletionTimeout)
				models, _ := venice.FetchImageModelsAt(ctx, httpClient(), apiKey, baseURL)
				cancel()
				if len(models) > 0 {
					if !custom {
						_ = cache.WriteModelInfoCache(veniceImageCacheKey, models)
					}
					cached = &cache.ModelCache{ModelInfos: models}
				}
			}
		}
	}
	var ids []string
	if cached != nil {
		for _, model := range cached.ModelInfos {
			if modelType == "" || model.Type == modelType {
				ids = append(ids, model.ID)
			}
		}
		// Older untyped cache entries are still useful for combined suggestions.
		if len(cached.ModelInfos) == 0 && modelType == "" {
			ids = append(ids, cached.Models...)
		}
	}
	if cfg != nil {
		if modelType == "" || modelType == "image" {
			ids = append(ids, cfg.Image.Venice.Model)
		}
		if modelType == "" || modelType == "inpaint" {
			ids = append(ids, cfg.Image.Venice.EditModel)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 0 && ids[0] == "" {
		ids = ids[1:]
	}
	return ids
}

// Completion never evaluates deferred endpoint references or falls back to the
// public cache when an explicit endpoint has been configured.
func veniceCompletionBaseURL(cfg *config.Config) (base string, custom, safe bool) {
	raw := ""
	if cfg != nil {
		raw = strings.TrimSpace(cfg.Image.Venice.BaseURL)
		if raw == "" {
			if pc := cfg.GetProviderConfig("venice"); pc != nil && config.InferProviderType("venice", pc.Type) == config.ProviderTypeVenice {
				raw = strings.TrimSpace(pc.BaseURL)
			}
		}
	}
	if raw == "" {
		return venice.BaseURL, false, true
	}
	if config.Cred(raw).IsDeferred() {
		return "", true, false
	}
	base, err := config.ResolveBaseURL("image.venice.base_url", raw, "")
	return base, true, err == nil && base != ""
}
