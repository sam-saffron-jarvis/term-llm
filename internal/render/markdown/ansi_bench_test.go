package markdown

import (
	"strings"
	"testing"
)

func BenchmarkANSIRenderLargeNoTabs(b *testing.B) {
	source := strings.Repeat("## Heading\n\nParagraph with **bold** text, `code`, and a link to [example](https://example.com) that wraps across the terminal width.\n\n", 128)
	renderer := NewANSI(Config{
		Palette:           testPalette,
		Width:             100,
		WrapOffset:        2,
		NormalizeTabs:     true,
		NormalizeNewlines: true,
		TrimSpace:         true,
	})
	input := []byte(source)

	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := renderer.Render(input); err != nil {
			b.Fatal(err)
		}
	}
}
