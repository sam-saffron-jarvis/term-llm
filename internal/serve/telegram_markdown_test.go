package serve

import (
	"strings"
	"testing"
)

func TestMdToTelegramHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			name:     "bold",
			input:    "This is **bold** text",
			contains: []string{"<b>bold</b>"},
			absent:   []string{"**"},
		},
		{
			name:     "italic",
			input:    "This is _italic_ text",
			contains: []string{"<i>italic</i>"},
			absent:   []string{"_italic_"},
		},
		{
			name:     "inline code",
			input:    "Use `fmt.Println` to print",
			contains: []string{"<code>fmt.Println</code>"},
			absent:   []string{"`"},
		},
		{
			name:     "code block",
			input:    "```go\nfmt.Println(\"hello\")\n```",
			contains: []string{"<pre>", "</pre>", "fmt.Println"},
			absent:   []string{"```"},
		},
		{
			name:     "strikethrough",
			input:    "~~deleted~~",
			contains: []string{"<s>deleted</s>"},
			absent:   []string{"~~"},
		},
		{
			name:     "link",
			input:    "[Click here](https://example.com)",
			contains: []string{`<a href="https://example.com">Click here</a>`},
		},
		{
			name:     "heading becomes bold",
			input:    "# Big Title",
			contains: []string{"<b>Big Title</b>"},
			absent:   []string{"<h1>"},
		},
		{
			name:     "unordered list",
			input:    "- Item 1\n- Item 2\n- Item 3",
			contains: []string{"• Item 1", "• Item 2", "• Item 3"},
			absent:   []string{"<ul>", "<li>"},
		},
		{
			name:     "ordered list",
			input:    "1. First\n2. Second\n3. Third",
			contains: []string{"1. First", "2. Second", "3. Third"},
			absent:   []string{"<ol>", "<li>"},
		},
		{
			name:     "blockquote",
			input:    "> A quoted passage",
			contains: []string{"<blockquote>", "A quoted passage", "</blockquote>"},
		},
		{
			name:     "no double asterisks in output",
			input:    "**hello** and __world__",
			contains: []string{"<b>hello</b>"},
			absent:   []string{"**", "__"},
		},
		{
			name:  "empty input",
			input: "",
		},
		{
			name:     "plain text passes through",
			input:    "Just plain text here",
			contains: []string{"Just plain text here"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mdToTelegramHTML(tc.input)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q to contain %q\ngot: %s", tc.input, want, got)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("expected %q NOT to contain %q\ngot: %s", tc.input, unwanted, got)
				}
			}
		})
	}
}
