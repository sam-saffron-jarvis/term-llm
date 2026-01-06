package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EditRetryDiagnostic contains diagnostic data for a failed edit that triggered a retry.
type EditRetryDiagnostic struct {
	Timestamp     time.Time `json:"timestamp"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	FilePath      string    `json:"file_path"`
	AttemptNumber int       `json:"attempt_number"`
	Reason        string    `json:"reason"`

	// Full context
	SystemPrompt string   `json:"system_prompt"`
	UserPrompt   string   `json:"user_prompt"`
	LLMResponse  string   `json:"llm_response"`            // partial output before failure
	FailedSearch string   `json:"failed_search,omitempty"` // for search/replace failures
	DiffLines    []string `json:"diff_lines,omitempty"`    // for unified diff failures
	FileContent  string   `json:"file_content"`            // current file state
}

// WriteEditRetry writes diagnostic data for a failed edit retry.
// Creates both a JSON file and a human-readable markdown file.
func WriteEditRetry(dir string, diag *EditRetryDiagnostic) error {
	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create diagnostics directory: %w", err)
	}

	// Generate base filename from timestamp
	ts := diag.Timestamp.Format("2006-01-02T15-04-05")
	baseName := fmt.Sprintf("edit-retry-%s", ts)

	// Write JSON file
	jsonPath := filepath.Join(dir, baseName+".json")
	if err := writeJSON(jsonPath, diag); err != nil {
		return err
	}

	// Write Markdown file
	mdPath := filepath.Join(dir, baseName+".md")
	if err := writeMarkdown(mdPath, diag); err != nil {
		return err
	}

	return nil
}

func writeJSON(path string, diag *EditRetryDiagnostic) error {
	data, err := json.MarshalIndent(diag, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal diagnostics: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write diagnostics JSON: %w", err)
	}
	return nil
}

func writeMarkdown(path string, diag *EditRetryDiagnostic) error {
	var b strings.Builder

	// Header
	b.WriteString("# Edit Retry Diagnostic\n\n")
	b.WriteString(fmt.Sprintf("**Timestamp:** %s\n", diag.Timestamp.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("**Provider:** %s\n", diag.Provider))
	b.WriteString(fmt.Sprintf("**Model:** %s\n", diag.Model))
	b.WriteString(fmt.Sprintf("**File:** %s\n", diag.FilePath))
	b.WriteString(fmt.Sprintf("**Attempt:** %d\n", diag.AttemptNumber))
	b.WriteString(fmt.Sprintf("**Reason:** %s\n", diag.Reason))
	b.WriteString("\n---\n\n")

	// Failed search pattern (if present)
	if diag.FailedSearch != "" {
		b.WriteString("## Failed Search Pattern\n\n")
		b.WriteString("```\n")
		b.WriteString(diag.FailedSearch)
		if !strings.HasSuffix(diag.FailedSearch, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
		b.WriteString("---\n\n")
	}

	// Diff lines (if present)
	if len(diag.DiffLines) > 0 {
		b.WriteString("## Failed Diff\n\n")
		b.WriteString("```diff\n")
		for _, line := range diag.DiffLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
		b.WriteString("---\n\n")
	}

	// LLM Response
	b.WriteString("## LLM Response (partial)\n\n")
	b.WriteString("```\n")
	b.WriteString(diag.LLMResponse)
	if !strings.HasSuffix(diag.LLMResponse, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	// File content
	b.WriteString("## File Content\n\n")
	ext := filepath.Ext(diag.FilePath)
	lang := extToLang(ext)
	b.WriteString(fmt.Sprintf("```%s\n", lang))
	b.WriteString(diag.FileContent)
	if !strings.HasSuffix(diag.FileContent, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")
	b.WriteString("---\n\n")

	// System prompt
	b.WriteString("## System Prompt\n\n")
	b.WriteString(diag.SystemPrompt)
	if !strings.HasSuffix(diag.SystemPrompt, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n---\n\n")

	// User prompt
	b.WriteString("## User Prompt\n\n")
	b.WriteString(diag.UserPrompt)
	if !strings.HasSuffix(diag.UserPrompt, "\n") {
		b.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return fmt.Errorf("failed to write diagnostics markdown: %w", err)
	}
	return nil
}

// extToLang maps file extensions to markdown code fence language hints.
func extToLang(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".jsx":
		return "jsx"
	case ".tsx":
		return "tsx"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt":
		return "kotlin"
	case ".sh", ".bash":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".xml":
		return "xml"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	case ".md":
		return "markdown"
	default:
		return ""
	}
}
