package streaming

import (
	"bytes"
	"strings"
	"testing"
)

func BenchmarkStreamRendererLargeNoTabs(b *testing.B) {
	block := "## Heading\n\nParagraph with **bold** text, `code`, and a link to [example](https://example.com) that wraps across the terminal width.\n\n"
	input := []byte(strings.Repeat(block, 64))
	chunks := bytes.SplitAfter(input, []byte("\n"))

	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		sr, err := NewRenderer(&buf, newTestMarkdownRenderer(testRenderWidth))
		if err != nil {
			b.Fatal(err)
		}
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if _, err := sr.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		if err := sr.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
