package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

// rendererCache provides width-keyed caching of glamour renderers.
// Creating a renderer is expensive; caching by width avoids recreation.
var rendererCache sync.Map // map[int]*glamour.TermRenderer

// getRenderer returns a cached renderer for the given width, creating one if needed.
func getRenderer(width int) (*glamour.TermRenderer, error) {
	if cached, ok := rendererCache.Load(width); ok {
		return cached.(*glamour.TermRenderer), nil
	}

	style := GlamourStyle()
	margin := uint(0)
	style.Document.Margin = &margin
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.CodeBlock.Margin = &margin

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	// Store for future use (race-safe: if another goroutine stored first, we just discard ours)
	rendererCache.Store(width, renderer)
	return renderer, nil
}

// RenderMarkdown renders markdown content using glamour with standard styling.
// This is the main function for rendering markdown in streaming contexts.
// On error, returns the original content unchanged.
func RenderMarkdown(content string, width int) string {
	if content == "" {
		return ""
	}

	rendered, err := RenderMarkdownWithError(content, width)
	if err != nil {
		return content
	}
	return rendered
}

// RenderMarkdownWithError renders markdown content and returns any errors.
// Use this variant when error handling is needed.
func RenderMarkdownWithError(content string, width int) (string, error) {
	renderer, err := getRenderer(width)
	if err != nil {
		return "", err
	}

	rendered, err := renderer.Render(content)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(rendered), nil
}
