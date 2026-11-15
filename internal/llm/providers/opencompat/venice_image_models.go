package opencompat

import (
	"slices"
	"strings"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/venice"
)

const (
	veniceImageCacheKey = venice.ImageCacheKey
)

// GetVeniceImageModelIDs offers cached/configured media IDs without resolving
// credentials or fetching catalogs. Explicit models --type refreshes the cache.
func GetVeniceImageModelIDs(cfg *config.Config, modelType string) []string {
	baseURL, _, safe := veniceCompletionBaseURL(cfg)
	var cached *cache.ModelCache
	if safe {
		cached, _ = cache.ReadModelCache(venice.MediaCacheKey(baseURL))
	}
	var ids []string
	if cached != nil {
		for _, model := range cached.ModelInfos {
			if (model.Type == "image" || model.Type == "inpaint") && (modelType == "" || model.Type == modelType) {
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
