package filetrack

import (
	"strings"
	"testing"
)

func TestBuildHunks(t *testing.T) {
	oldContent := []byte("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n")
	newContent := []byte("line1\nline2\nCHANGED\nline4\nline5\nline6\nline7\nline8\nline9\nADDED\nline10\n")

	hunks := BuildHunks("test.txt", oldContent, newContent)
	if len(hunks) == 0 {
		t.Fatal("expected hunks for differing content")
	}

	if hunks[0].OldStart < 1 || hunks[0].NewStart < 1 {
		t.Fatalf("hunk starts = %d/%d, want 1-indexed", hunks[0].OldStart, hunks[0].NewStart)
	}

	var adds, dels, ctx int
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.T {
			case "add":
				adds++
			case "del":
				dels++
			case "ctx":
				ctx++
			default:
				t.Fatalf("unknown line type %q", l.T)
			}
		}
	}
	if adds != 2 || dels != 1 {
		t.Fatalf("adds/dels = %d/%d, want 2/1", adds, dels)
	}
	if ctx == 0 {
		t.Fatal("expected context lines")
	}
}

func TestBuildHunksIdenticalContent(t *testing.T) {
	content := []byte("same\n")
	if hunks := BuildHunks("f", content, content); hunks != nil {
		t.Fatalf("hunks = %+v, want nil for identical content", hunks)
	}
}

func TestBuildHunksLineText(t *testing.T) {
	hunks := BuildHunks("f", []byte("old line\n"), []byte("new line\n"))
	var foundDel, foundAdd bool
	for _, h := range hunks {
		for _, l := range h.Lines {
			if l.T == "del" && l.S == "old line" {
				foundDel = true
			}
			if l.T == "add" && l.S == "new line" {
				foundAdd = true
			}
		}
	}
	if !foundDel || !foundAdd {
		t.Fatalf("expected prefix-stripped del/add lines, got %+v", hunks)
	}
}

func TestCountAddsDels(t *testing.T) {
	tests := []struct {
		name       string
		old, new   string
		adds, dels int
	}{
		{"create", "", "a\nb\nc\n", 3, 0},
		{"create without trailing newline", "", "a\nb", 2, 0},
		{"delete", "a\nb\n", "", 0, 2},
		{"modify", "a\nb\nc\n", "a\nX\nc\n", 1, 1},
		{"empty both", "", "", 0, 0},
		{"pure addition", "a\n", "a\nb\n", 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adds, dels := CountAddsDels([]byte(tt.old), []byte(tt.new))
			if adds != tt.adds || dels != tt.dels {
				t.Fatalf("adds/dels = %d/%d, want %d/%d", adds, dels, tt.adds, tt.dels)
			}
		})
	}
}

func TestCountAddsDelsLargeChange(t *testing.T) {
	oldContent := strings.Repeat("shared\n", 50) + "removed1\nremoved2\n"
	newContent := strings.Repeat("shared\n", 50) + "added1\nadded2\nadded3\n"
	adds, dels := CountAddsDels([]byte(oldContent), []byte(newContent))
	if adds != 3 || dels != 2 {
		t.Fatalf("adds/dels = %d/%d, want 3/2", adds, dels)
	}
}
