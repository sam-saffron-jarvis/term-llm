package prompt

import "testing"

func TestExtractLineRange(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"

	tests := []struct {
		name      string
		startLine int
		endLine   int
		want      string
	}{
		{"lines 2-4", 2, 4, "2: line2\n3: line3\n4: line4"},
		{"line 3 only", 3, 3, "3: line3"},
		{"all lines", 1, 5, "1: line1\n2: line2\n3: line3\n4: line4\n5: line5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLineRange(content, tc.startLine, tc.endLine)
			if got != tc.want {
				t.Errorf("extractLineRange(%d, %d):\nwant: %q\ngot:  %q",
					tc.startLine, tc.endLine, tc.want, got)
			}
		})
	}
}
