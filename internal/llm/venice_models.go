package llm

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/samsaffron/term-llm/internal/cache"
)

const veniceCacheKey = "venice"

var veniceCacheRefreshInFlight atomic.Bool

func GetCachedVeniceModels(apiKey string) []string {
	models := GetCachedVeniceModelInfos(apiKey)
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

func GetCachedVeniceModelInfos(apiKey string) []ModelInfo {
	cached, err := cache.ReadModelCache(veniceCacheKey)
	if err == nil && cache.IsCacheValid(cached) {
		return modelInfosFromCache(cached)
	}

	if apiKey == "" {
		apiKey = os.Getenv("VENICE_API_KEY")
	}
	if apiKey == "" {
		if cached != nil && len(cached.Models) > 0 {
			return modelInfosFromCache(cached)
		}
		return nil
	}

	if cached != nil && len(cached.Models) > 0 {
		if veniceCacheRefreshInFlight.CompareAndSwap(false, true) {
			go refreshVeniceCache(apiKey)
		}
		return modelInfosFromCache(cached)
	}

	return fetchVeniceModelInfosSync(apiKey)
}

func fetchVeniceModelInfosSync(apiKey string) []ModelInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := NewVeniceProvider(apiKey, "").ListModels(ctx)
	if err != nil || len(models) == 0 {
		return nil
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func refreshVeniceCache(apiKey string) {
	defer veniceCacheRefreshInFlight.Store(false)
	_ = fetchVeniceModelInfosSync(apiKey)
}

func RefreshVeniceCacheSync(models []ModelInfo) {
	if len(models) == 0 {
		return
	}
	modelInfos := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		if strings.TrimSpace(m.ID) != "" {
			modelInfos = append(modelInfos, m)
		}
	}
	sort.Slice(modelInfos, func(i, j int) bool { return modelInfos[i].ID < modelInfos[j].ID })
	_ = cache.WriteModelInfoCache(veniceCacheKey, modelInfosToCache(modelInfos))
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
