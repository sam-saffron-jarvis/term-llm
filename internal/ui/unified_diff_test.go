package ui

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"testing"
)

func TestUnifiedDiffLineNumbers(t *testing.T) {
	tests := []struct {
		name       string
		oldContent string
		newContent string
		wantLines  []string // Expected line number prefixes in order
	}{
		{
			name:       "replacement with fewer lines",
			oldContent: "line1\nold2\nold3\nold4\nline5\n",
			newContent: "line1\nnew2\nline5\n",
			// Context line1 (1), delete old2-old4 (virtual 2,3,4), add new2 (2), context line5 (3)
			wantLines: []string{"1 ", "2-", "3-", "4-", "2+", "3 "},
		},
		{
			name:       "replacement with more lines",
			oldContent: "line1\nold2\nline3\n",
			newContent: "line1\nnew2\nnew3\nnew4\nline3\n",
			// Context line1 (1), delete old2 (2), add new2-new4 (2-4), context line3 (5)
			wantLines: []string{"1 ", "2-", "2+", "3+", "4+", "5 "},
		},
		{
			name:       "pure deletion",
			oldContent: "line1\ndelete_me\nline3\n",
			newContent: "line1\nline3\n",
			// Context line1 (1), delete delete_me (2), context line3 (2)
			wantLines: []string{"1 ", "2-", "2 "},
		},
		{
			name:       "pure addition",
			oldContent: "line1\nline2\n",
			newContent: "line1\nnew_line\nline2\n",
			// Context line1 (1), add new_line (2), context line2 (3)
			wantLines: []string{"1 ", "2+", "3 "},
		},
	}

	// Regex to extract line numbers from ANSI-colored output
	// Matches patterns like "  1  " (context), "  2- " (deletion), "  3+ " (addition)
	lineNumRe := regexp.MustCompile(`(\d+)([-+ ]) `)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			PrintUnifiedDiff("test.txt", tt.oldContent, tt.newContent)

			w.Close()
			os.Stdout = old

			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Extract all line number prefixes
			matches := lineNumRe.FindAllStringSubmatch(output, -1)

			var gotLines []string
			for _, m := range matches {
				gotLines = append(gotLines, m[1]+m[2])
			}

			// Compare
			if len(gotLines) != len(tt.wantLines) {
				t.Errorf("got %d line prefixes, want %d\ngot:  %v\nwant: %v",
					len(gotLines), len(tt.wantLines), gotLines, tt.wantLines)
				return
			}

			for i := range tt.wantLines {
				if gotLines[i] != tt.wantLines[i] {
					t.Errorf("line %d: got %q, want %q\nfull got:  %v\nfull want: %v",
						i, gotLines[i], tt.wantLines[i], gotLines, tt.wantLines)
				}
			}
		})
	}
}
