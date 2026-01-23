package ui

import (
	"sync"

	"github.com/samsaffron/term-llm/internal/image"
)

// renderedImages tracks which images have been rendered to prevent duplicates
var (
	renderedImages   = make(map[string]bool)
	renderedImagesMu sync.Mutex
)

// RenderInlineImage renders an image as terminal escape sequences for inline display.
// Each image path is only rendered once - subsequent calls return empty string.
// Returns empty string on error or if terminal doesn't support images.
func RenderInlineImage(path string) string {
	if path == "" {
		return ""
	}

	// Check if already rendered
	renderedImagesMu.Lock()
	if renderedImages[path] {
		renderedImagesMu.Unlock()
		return ""
	}
	// Mark as rendered before we actually render (prevents race conditions)
	renderedImages[path] = true
	renderedImagesMu.Unlock()

	// Render the image
	result, err := image.RenderImageToString(path)
	if err != nil || result.Full == "" {
		return ""
	}

	return result.Full
}

// ClearRenderedImages clears the tracking of rendered images.
// Call this when starting a new session.
func ClearRenderedImages() {
	renderedImagesMu.Lock()
	renderedImages = make(map[string]bool)
	renderedImagesMu.Unlock()
}
