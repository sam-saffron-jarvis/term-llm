package llm

import (
	"strings"

	"github.com/samsaffron/term-llm/internal/cache"
)

const veniceCacheKey = "venice"

func RefreshVeniceCacheSync(models []ModelInfo) {
	if len(models) == 0 {
		return
	}
	_ = cache.WriteModelInfoCache(veniceCacheKey, modelInfosToCache(models))
}

func veniceCachedInputLimit(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return 0
	}
	cached, err := cache.ReadModelCache(veniceCacheKey)
	if err != nil || cached == nil {
		return 0
	}
	for _, m := range cached.ModelInfos {
		if strings.ToLower(strings.TrimSpace(m.ID)) == model {
			return m.InputLimit
		}
	}
	return 0
}
