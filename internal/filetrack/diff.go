package filetrack

import (
	"regexp"
	"strconv"
	"strings"

	diff "github.com/shogoki/gotextdiff"
)

// DiffLine is one row of a structured diff hunk.
type DiffLine struct {
	T string `json:"t"` // "ctx" | "add" | "del"
	S string `json:"s"` // line text without the diff prefix
}

// Hunk is one contiguous block of a structured diff.
type Hunk struct {
	OldStart int        `json:"old_start"`
	NewStart int        `json:"new_start"`
	Lines    []DiffLine `json:"lines"`
}

// hunkHeaderRe parses "@@ -start,count +start,count @@" headers
// (same shape as internal/ui/unified_diff.go).
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// BuildHunks computes a structured diff between two file contents.
// Returns nil when the contents are identical.
func BuildHunks(path string, oldContent, newContent []byte) []Hunk {
	diffBytes := diff.Diff(path, oldContent, path, newContent)
	if len(diffBytes) == 0 {
		return nil
	}

	var hunks []Hunk
	var current *Hunk
	for _, line := range strings.Split(string(diffBytes), "\n") {
		if line == "" || strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}

		switch line[0] {
		case '@':
			matches := hunkHeaderRe.FindStringSubmatch(line)
			if matches == nil {
				continue
			}
			oldStart, _ := strconv.Atoi(matches[1])
			newStart, _ := strconv.Atoi(matches[2])
			hunks = append(hunks, Hunk{OldStart: oldStart, NewStart: newStart})
			current = &hunks[len(hunks)-1]
		case '-':
			if current != nil {
				current.Lines = append(current.Lines, DiffLine{T: "del", S: line[1:]})
			}
		case '+':
			if current != nil {
				current.Lines = append(current.Lines, DiffLine{T: "add", S: line[1:]})
			}
		case ' ':
			if current != nil {
				current.Lines = append(current.Lines, DiffLine{T: "ctx", S: line[1:]})
			}
		}
		// '\ No newline at end of file' and anything unknown is skipped.
	}
	return hunks
}

// CountAddsDels counts added and removed lines between two contents.
// Empty/nil sides are treated as a missing file (pure create/delete).
func CountAddsDels(oldContent, newContent []byte) (adds, dels int) {
	if len(oldContent) == 0 && len(newContent) == 0 {
		return 0, 0
	}
	if len(oldContent) == 0 {
		return countLines(newContent), 0
	}
	if len(newContent) == 0 {
		return 0, countLines(oldContent)
	}

	for _, hunk := range BuildHunks("file", oldContent, newContent) {
		for _, line := range hunk.Lines {
			switch line.T {
			case "add":
				adds++
			case "del":
				dels++
			}
		}
	}
	return adds, dels
}

func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := strings.Count(string(content), "\n")
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}
