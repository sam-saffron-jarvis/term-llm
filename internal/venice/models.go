// Package venice shares Venice's explicit media catalog and offline constraints.
package venice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/providerhttp"
)

const (
	BaseURL               = "https://api.venice.ai/api/v1"
	ImageCacheKey         = "venice-image"
	MaxCatalogBytes int64 = 8 << 20
)

// MediaCacheKey scopes catalogs to a resolved normalized root, never a credential.
func MediaCacheKey(root string) string {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	if root == BaseURL {
		return ImageCacheKey
	}
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("venice-media-%x", sum[:16])
}

// FetchImageModels keeps the existing discovery API without writing any cache.
func FetchImageModels(ctx context.Context, client *http.Client, key string) ([]cache.CachedModel, error) {
	return FetchImageModelsAt(ctx, client, key, BaseURL)
}
func FetchImageModelsAt(ctx context.Context, client *http.Client, key, root string) ([]cache.CachedModel, error) {
	models, err := FetchMediaModels(ctx, client, key, root, "all")
	return slices.DeleteFunc(models, func(m cache.CachedModel) bool { return m.Type != "image" && m.Type != "inpaint" }), err
}

// FetchMediaModels is used only by explicit model listing, never media execution.
func FetchMediaModels(ctx context.Context, client *http.Client, key, root, kind string) ([]cache.CachedModel, error) {
	if kind != "image" && kind != "inpaint" && kind != "video" && kind != "all" {
		return nil, fmt.Errorf("unsupported media catalog type")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(root, "/")+"/models?type="+kind, nil)
	if err != nil {
		return nil, fmt.Errorf("create Venice catalog request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+config.NormalizeVeniceAPIKey(key))
	safe := http.Client{}
	if client != nil {
		safe = *client
	}
	safe.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safe.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Venice catalog: %w", err)
	}
	defer resp.Body.Close()
	body, err := providerhttp.ReadBounded(resp.Body, MaxCatalogBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, StatusError(resp, body, config.NormalizeVeniceAPIKey(key))
	}
	var catalog struct {
		Data []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			ModelSpec struct {
				Name        string          `json:"name"`
				Privacy     string          `json:"privacy"`
				Constraints json.RawMessage `json:"constraints"`
			} `json:"model_spec"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("decode Venice catalog: %w", err)
	}
	models := []cache.CachedModel{}
	seen := map[string]bool{}
	for _, m := range catalog.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || (m.Type != "image" && m.Type != "inpaint" && m.Type != "video") || (kind != "all" && m.Type != kind) || seen[m.Type+"/"+id] {
			continue
		}
		seen[m.Type+"/"+id] = true
		meta := &cache.MediaModelMetadata{Privacy: m.ModelSpec.Privacy, FetchedAt: time.Now()}
		model := cache.CachedModel{ID: id, Type: m.Type, DisplayName: m.ModelSpec.Name, Media: meta}
		if c := parseConstraints(m.ModelSpec.Constraints); c != nil {
			meta.ModelType = c.ModelType
			meta.Durations = constraintValues(c.Durations, normalizeSeconds)
			if m.Type == "video" {
				meta.Resolutions = constraintValues(c.Resolutions, normalizeResolution)
			} else {
				meta.Resolutions = constraintValues(c.Resolutions, func(s string) (string, bool) { return s, strings.TrimSpace(s) != "" })
			}
			if m.Type != "video" {
				model.ImageConstraints = &cache.ImageModelConstraints{Resolutions: meta.Resolutions}
			}
		}
		models = append(models, model)
	}
	sortModels(models)
	return models, nil
}

func sortModels(models []cache.CachedModel) {
	slices.SortFunc(models, func(a, b cache.CachedModel) int {
		if a.Type != b.Type {
			return strings.Compare(a.Type, b.Type)
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// StoreMediaModels replaces one explicitly refreshed type without refreshing others.
func StoreMediaModels(root, kind string, models []cache.CachedModel) error {
	old, _ := cache.ReadModelCache(MediaCacheKey(root))
	combined := []cache.CachedModel{}
	if old != nil {
		for _, m := range old.ModelInfos {
			if m.Type != kind {
				// Preserve legacy freshness when upgrading the shared cache format.
				if m.Media == nil {
					m.Media = &cache.MediaModelMetadata{FetchedAt: old.FetchedAt}
				}
				combined = append(combined, m)
			}
		}
	}
	now := time.Now()
	for _, m := range models {
		if m.Type == kind {
			if m.Media == nil {
				m.Media = &cache.MediaModelMetadata{}
			}
			m.Media.FetchedAt = now
			combined = append(combined, m)
		}
	}
	sortModels(combined)
	return cache.WriteModelInfoCache(MediaCacheKey(root), combined)
}

// CachedMediaModels is read-only. Stale/missing constraints are never authoritative.
func CachedMediaModels(root string) []cache.CachedModel {
	stored, _ := cache.ReadModelCache(MediaCacheKey(root))
	if stored == nil {
		return nil
	}
	models := []cache.CachedModel{}
	for _, m := range stored.ModelInfos {
		at := stored.FetchedAt
		if m.Media != nil {
			at = m.Media.FetchedAt
		}
		if !at.IsZero() && time.Since(at) < cache.ModelCacheTTL {
			models = append(models, m)
		}
	}
	return models
}

func CachedImageModels(root string) []cache.CachedModel {
	return slices.DeleteFunc(CachedMediaModels(root), func(m cache.CachedModel) bool { return m.Type != "image" && m.Type != "inpaint" })
}

// ValidateVideoOptions uses only fresh cached evidence; absence is not rejection.
func ValidateVideoOptions(root, model, duration string, resolution *string) error {
	for _, m := range CachedMediaModels(root) {
		if m.Type != "video" || m.ID != model || m.Media == nil {
			continue
		}
		durations := normalizedStrings(m.Media.Durations, normalizeSeconds)
		requested, _ := normalizeSeconds(duration)
		if len(durations) > 0 && !slices.Contains(durations, requested) {
			return fmt.Errorf("duration %s is not in the cached constraints for %s", duration, model)
		}
		resolutions := normalizedStrings(m.Media.Resolutions, normalizeResolution)
		requestedResolution := ""
		if resolution != nil {
			requestedResolution, _ = normalizeResolution(*resolution)
		}
		if resolution != nil && len(resolutions) > 0 && !slices.Contains(resolutions, requestedResolution) {
			return fmt.Errorf("resolution %s is not in the cached constraints for %s", *resolution, model)
		}
	}
	return nil
}
