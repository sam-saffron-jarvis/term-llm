package engine

import "testing"

func TestTruncateWordsPreservesMarkdownStructure(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		max            int
	}{
		{"under limit keeps newlines", "- one two\n- three\n", "- one two\n- three", 10},
		{"over limit cuts at word boundary", "- one two\n- three four\n- five", "- one two\n- three …", 5},
		{"no limit", "  a\n\nb  ", "a\n\nb", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateWords(tc.in, tc.max); got != tc.want {
				t.Fatalf("truncateWords(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}
