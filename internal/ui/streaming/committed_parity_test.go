package streaming

import (
	"bytes"
	"testing"
)

func TestCommittedRenderedMatchesDirectAtStreamingBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		partial bool
	}{
		{
			name:  "positive incremental paragraphs and heading",
			input: "Alpha.\n\n## Heading\n\nBeta.\n\n```go\nfmt.Println(\"hi\")\n```\n",
		},
		{
			name:    "positive incremental production partial mode",
			input:   "Alpha.\n\n## Heading\n\nBeta.\n\n```go\nfmt.Println(\"hi\")\n```\n",
			partial: true,
		},
		{
			name:  "tight list sibling flush stays tight",
			input: "- a\n- b\n- c\n",
		},
		{
			name:  "indented heading-looking line remains paragraph continuation",
			input: "foo\n    # bar\n",
		},
		{
			name:  "indented thematic-looking line remains paragraph continuation",
			input: "Foo\n    ***\n",
		},
		{
			name:  "blockquote reference definition can rewrite earlier shortcut ref",
			input: "[foo]\n\n> [foo]: https://example.com\n\nAfter\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr := newCommittedParityRenderer(t, &bytes.Buffer{}, tt.partial)
			for i := 0; i < len(tt.input); i++ {
				if _, err := sr.Write([]byte{tt.input[i]}); err != nil {
					t.Fatalf("write byte %d failed: %v", i, err)
				}
				assertCommittedRenderedMatchesDirect(t, sr, []byte(tt.input[:i+1]))
			}
		})
	}
}

func TestCorpusCommittedRenderedMatchesDirectAtStreamingBoundaries(t *testing.T) {
	for _, doc := range loadCorpusDocuments(t) {
		for _, partial := range []bool{false, true} {
			name := doc.name
			if partial {
				name += "/partial"
			} else {
				name += "/plain"
			}
			t.Run(name, func(t *testing.T) {
				sr := newCommittedParityRenderer(t, &bytes.Buffer{}, partial)
				for i := 0; i < len(doc.content); i++ {
					if _, err := sr.Write(doc.content[i : i+1]); err != nil {
						t.Fatalf("write byte %d failed: %v", i, err)
					}
					assertCommittedRenderedMatchesDirect(t, sr, doc.content[:i+1])
				}
			})
		}
	}
}

func newCommittedParityRenderer(t testing.TB, w *bytes.Buffer, partial bool) *StreamRenderer {
	t.Helper()
	if partial {
		sr, err := NewRendererWithOptions(
			w,
			newTestMarkdownRenderer(testRenderWidth),
			[]StreamRendererOption{WithPartialRendering()},
		)
		if err != nil {
			t.Fatalf("NewRendererWithOptions failed: %v", err)
		}
		return sr
	}
	sr, err := NewRenderer(w, newTestMarkdownRenderer(testRenderWidth))
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	return sr
}

func assertCommittedRenderedMatchesDirect(t testing.TB, sr *StreamRenderer, inputPrefix []byte) {
	t.Helper()
	committedLen := sr.CommittedMarkdownLen()
	if committedLen == 0 {
		if got := sr.CommittedRendered(); got != "" {
			t.Fatalf("CommittedRendered() = %q, want empty", got)
		}
		return
	}
	if committedLen > len(inputPrefix) {
		t.Fatalf("committed length %d exceeds input prefix %d", committedLen, len(inputPrefix))
	}
	want := renderDirectBytes(t, inputPrefix[:committedLen])
	got := []byte(sr.CommittedRendered())
	assertRenderedEqual(t, want, got, [][]byte{inputPrefix})
}
