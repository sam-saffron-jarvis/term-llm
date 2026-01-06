package prompt

import (
	"fmt"
	"strings"
)

// EditSpec describes a file with optional line range guard.
type EditSpec struct {
	Path      string
	StartLine int // 1-indexed, 0 means from beginning
	EndLine   int // 1-indexed, 0 means to end
	HasGuard  bool
}

// extractLineRange extracts lines startLine to endLine (1-indexed, inclusive) from content.
func extractLineRange(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")

	// Adjust for 0-based indexing
	start := startLine - 1
	if start < 0 {
		start = 0
	}
	end := endLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start >= len(lines) {
		return ""
	}

	// Build output with line numbers
	var sb strings.Builder
	for i := start; i < end; i++ {
		sb.WriteString(fmt.Sprintf("%d: %s\n", i+1, lines[i]))
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
