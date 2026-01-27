package ui

import (
	"sync"

	"github.com/samsaffron/term-llm/internal/image"
)

// maxImageCacheSize is the maximum number of entries in the image cache
const maxImageCacheSize = 100

// renderedImages caches rendered image output to avoid re-encoding
var (
	renderedImages      = make(map[string]string)
	renderedImagesOrder []string // tracks insertion order for LRU eviction
	renderedImagesMu    sync.Mutex
)

// RenderInlineImage renders an image as terminal escape sequences for inline display.
// The rendered output is cached, so subsequent calls return the cached result.
// Returns empty string on error or if terminal doesn't support images.
// The cache is limited to maxImageCacheSize entries; oldest entries are evicted when full.
func RenderInlineImage(path string) string {
	if path == "" {
		return ""
	}

	// Check cache first
	renderedImagesMu.Lock()
	if cached, ok := renderedImages[path]; ok {
		renderedImagesMu.Unlock()
		return cached
	}
	renderedImagesMu.Unlock()

	// Render the image
	result, err := image.RenderImageToString(path)
	if err != nil || result.Full == "" {
		// Cache empty result to avoid repeated failed attempts
		renderedImagesMu.Lock()
		addToImageCache(path, "")
		renderedImagesMu.Unlock()
		return ""
	}

	// Cache the rendered output
	renderedImagesMu.Lock()
	addToImageCache(path, result.Full)
	renderedImagesMu.Unlock()

	return result.Full
}

// addToImageCache adds an entry to the cache, evicting oldest if at capacity.
// Must be called with renderedImagesMu held.
func addToImageCache(path, value string) {
	// If already in cache, just update the value (don't change order)
	if _, exists := renderedImages[path]; exists {
		renderedImages[path] = value
		return
	}

	// Evict oldest entries if at capacity
	for len(renderedImages) >= maxImageCacheSize && len(renderedImagesOrder) > 0 {
		oldest := renderedImagesOrder[0]
		renderedImagesOrder = renderedImagesOrder[1:]
		delete(renderedImages, oldest)
	}

	// Add new entry
	renderedImages[path] = value
	renderedImagesOrder = append(renderedImagesOrder, path)
}

// ClearRenderedImages clears the cache of rendered images.
// Call this when starting a new session.
func ClearRenderedImages() {
	renderedImagesMu.Lock()
	renderedImages = make(map[string]string)
	renderedImagesOrder = nil
	renderedImagesMu.Unlock()
}
