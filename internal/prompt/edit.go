package prompt

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/samsaffron/term-llm/internal/input"
)

// EditSpec describes a file with optional line range guard.
type EditSpec struct {
	Path      string
	StartLine int // 1-indexed, 0 means from beginning
	EndLine   int // 1-indexed, 0 means to end
	HasGuard  bool
}

// EditSystemPrompt builds a system prompt for the edit tool.
func EditSystemPrompt(instructions string, specs []EditSpec, wildcardToken string) string {
	cwd, _ := os.Getwd()
	base := fmt.Sprintf(`You are an expert code editor. Use the edit tool to make changes to files.

Context:
- Operating System: %s
- Architecture: %s
- Current Directory: %s`, runtime.GOOS, runtime.GOARCH, cwd)

	if instructions != "" {
		base += fmt.Sprintf("\n- User Context: %s", instructions)
	}

	base += fmt.Sprintf(`

Rules:
1. Make minimal, focused changes
2. Preserve existing code style
3. Use the edit tool for each change - you can call it multiple times
4. The edit tool does find/replace: old_string must match exactly
5. You may include the literal token %s in old_string to match any sequence of characters (including newlines)
6. Include enough context in old_string (especially around %s) to be unique`, wildcardToken, wildcardToken)

	// Add guard info
	hasGuards := false
	for _, spec := range specs {
		if spec.HasGuard {
			hasGuards = true
			base += fmt.Sprintf("\n\nIMPORTANT: For %s, only modify lines %d-%d. The <editable-region> block shows the exact content you may edit with line numbers.",
				spec.Path, spec.StartLine, spec.EndLine)
		}
	}
	if hasGuards {
		base += "\n\nYour old_string MUST match text within the editable region. Use the line numbers in <editable-region> to ensure your edit is within bounds."
	}

	return base
}

// UnifiedDiffSystemPrompt builds a system prompt for unified diff format.
func UnifiedDiffSystemPrompt(instructions string, specs []EditSpec) string {
	cwd, _ := os.Getwd()
	base := fmt.Sprintf(`You are an expert code editor. Use the unified_diff tool to make changes to files.

Context:
- Operating System: %s
- Architecture: %s
- Current Directory: %s`, runtime.GOOS, runtime.GOARCH, cwd)

	if instructions != "" {
		base += fmt.Sprintf("\n- User Context: %s", instructions)
	}

	base += `

UNIFIED DIFF FORMAT:

--- path/to/file
+++ path/to/file
@@ context to locate (e.g., func ProcessData) @@
 context line (space prefix = unchanged, used to find location)
-line being removed
+line being added

LINE PREFIXES:
- Space " " = context line (unchanged, anchors position)
- Minus "-" = line being removed from original
- Plus "+"  = line being added in replacement

ELISION (-...) FOR LARGE REPLACEMENTS:
When replacing 10+ lines, use -... instead of listing every removed line:

--- file.go
+++ file.go
@@ func BigFunction @@
-func BigFunction() error {
-...
-}
+func BigFunction() error {
+    return simplifiedImpl()
+}

CRITICAL: After -... you MUST have an end anchor (the -} above) so we know where elision stops.
The -... matches everything between -func BigFunction()... and -}.

SMALL CHANGES - LIST ALL LINES:
For changes under 10 lines, list each line explicitly:

--- file.go
+++ file.go
@@ func SmallFunc @@
 func SmallFunc() {
-    oldLine1()
-    oldLine2()
+    newLine1()
+    newLine2()
 }

ADDING NEW CODE (no - lines needed):

--- file.go
+++ file.go
@@ func Existing @@
 func Existing() {
     keepThis()
+    addedLine()
 }

MULTIPLE FILES: Use separate --- +++ blocks for each file.`

	// Add guard info
	for _, spec := range specs {
		if spec.HasGuard {
			base += fmt.Sprintf("\n\nIMPORTANT: For %s, only modify lines %d-%d.",
				spec.Path, spec.StartLine, spec.EndLine)
		}
	}

	return base
}

// EditUserPrompt builds the user prompt with file context and optional guarded regions.
func EditUserPrompt(request string, files []input.FileContent, specs []EditSpec) string {
	var sb strings.Builder

	sb.WriteString("Files:\n\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("<file path=\"%s\">\n", f.Path))
		sb.WriteString(f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("</file>\n\n")
	}

	// Add editable region blocks for guarded files
	for _, spec := range specs {
		if spec.HasGuard {
			for _, f := range files {
				if f.Path == spec.Path {
					excerpt := extractLineRange(f.Content, spec.StartLine, spec.EndLine)
					sb.WriteString(fmt.Sprintf("<editable-region path=\"%s\" lines=\"%d-%d\">\n",
						spec.Path, spec.StartLine, spec.EndLine))
					sb.WriteString(excerpt)
					if !strings.HasSuffix(excerpt, "\n") {
						sb.WriteString("\n")
					}
					sb.WriteString("</editable-region>\n\n")
					break
				}
			}
		}
	}

	sb.WriteString(fmt.Sprintf("Request: %s", request))
	return sb.String()
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
