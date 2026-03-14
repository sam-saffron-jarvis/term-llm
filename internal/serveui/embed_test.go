package serveui

import (
	"strings"
	"testing"
)

func TestStaticAssetsSupportEmbeddedVideoPlayback(t *testing.T) {
	renderJS, err := StaticAsset("app-render.js")
	if err != nil {
		t.Fatalf("StaticAsset(app-render.js): %v", err)
	}
	renderSrc := string(renderJS)
	for _, want := range []string{
		"ADD_TAGS: ['video', 'source']",
		"ADD_ATTR: ['controls', 'playsinline', 'muted', 'loop', 'autoplay', 'poster', 'preload']",
	} {
		if !strings.Contains(renderSrc, want) {
			t.Fatalf("app-render.js missing %q", want)
		}
	}

	css, err := StaticAsset("app.css")
	if err != nil {
		t.Fatalf("StaticAsset(app.css): %v", err)
	}
	cssSrc := string(css)
	for _, want := range []string{
		".markdown-body video",
		"max-width: 100%;",
	} {
		if !strings.Contains(cssSrc, want) {
			t.Fatalf("app.css missing %q", want)
		}
	}
}
