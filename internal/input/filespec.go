package input

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FileSpec represents a file with optional line range
type FileSpec struct {
	Path      string
	StartLine int  // 1-indexed, 0 means from beginning
	EndLine   int  // 1-indexed, 0 means to end
	HasRegion bool // true if a line range was specified
}

// ParseFileSpec parses a file specification like "main.go:11-22"
// Supported formats:
//   - main.go       - Entire file (no region)
//   - main.go:11-22 - Lines 11-22
//   - main.go:11-   - Lines 11 to end of file
//   - main.go:-22   - Lines 1-22
func ParseFileSpec(spec string) (FileSpec, error) {
	re := regexp.MustCompile(`^(.+?)(?::(\d*)-(\d*))?$`)
	matches := re.FindStringSubmatch(spec)
	if matches == nil {
		return FileSpec{}, fmt.Errorf("invalid file spec: %s", spec)
	}

	fs := FileSpec{Path: matches[1]}

	if strings.Contains(spec, ":") && len(matches) > 1 {
		fs.HasRegion = true
		if matches[2] != "" {
			start, err := strconv.Atoi(matches[2])
			if err != nil {
				return FileSpec{}, fmt.Errorf("invalid start line: %s", matches[2])
			}
			fs.StartLine = start
		}
		if matches[3] != "" {
			end, err := strconv.Atoi(matches[3])
			if err != nil {
				return FileSpec{}, fmt.Errorf("invalid end line: %s", matches[3])
			}
			fs.EndLine = end
		}
	}

	return fs, nil
}

// ExtractLines extracts lines from content based on start and end line numbers.
// Line numbers are 1-indexed. 0 for start means from beginning, 0 for end means to end.
func ExtractLines(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	// Convert to 0-indexed
	start := 0
	if startLine > 0 {
		start = startLine - 1
	}
	if start >= totalLines {
		return ""
	}

	end := totalLines
	if endLine > 0 && endLine < totalLines {
		end = endLine
	}

	if start >= end {
		return ""
	}

	return strings.Join(lines[start:end], "\n")
}

// FormatSpecPath returns a display path that includes the region if specified
func (fs FileSpec) FormatSpecPath() string {
	if !fs.HasRegion {
		return fs.Path
	}
	return fmt.Sprintf("%s:%d-%d", fs.Path, fs.StartLine, fs.EndLine)
}
