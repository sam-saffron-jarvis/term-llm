package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepTool_UsesFilePath(t *testing.T) {
	dir := t.TempDir()
	token := "unique_grep_token_1234567890"
	filePath := filepath.Join(dir, "sample.txt")

	if err := os.WriteFile(filePath, []byte("before "+token+" after"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	tool := NewGrepTool(nil, DefaultOutputLimits())
	args, err := json.Marshal(GrepArgs{
		Pattern:          token,
		Path:             dir,
		FilesWithMatches: true,
	})
	if err != nil {
		t.Fatalf("failed to marshal args: %v", err)
	}

	output, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(output.Content, filePath) {
		t.Fatalf("expected output to contain %q, got: %s", filePath, output.Content)
	}
}

func TestFormatGrepResults_SingleFile(t *testing.T) {
	matches := []GrepMatch{
		{FilePath: "pkg/foo.go", LineNumber: 10, Match: "func Foo()", Context: "  9: // Foo does things\n> 10: func Foo()\n  11: {"},
		{FilePath: "pkg/foo.go", LineNumber: 20, Match: "func Bar()", Context: "> 20: func Bar()"},
	}

	result := formatGrepResults(matches, false)

	// Summary line
	if !strings.Contains(result, "2 matches in 1 file") {
		t.Errorf("expected summary '2 matches in 1 file', got:\n%s", result)
	}

	// File header appears once
	count := strings.Count(result, "pkg/foo.go")
	if count != 1 {
		t.Errorf("expected file path to appear once, got %d times", count)
	}

	// File header shows match count
	if !strings.Contains(result, "pkg/foo.go (2 matches):") {
		t.Errorf("expected file header with count, got:\n%s", result)
	}

	// Context present
	if !strings.Contains(result, "func Foo()") || !strings.Contains(result, "func Bar()") {
		t.Errorf("expected both match lines in output, got:\n%s", result)
	}
}

func TestFormatGrepResults_MultipleFiles(t *testing.T) {
	matches := []GrepMatch{
		{FilePath: "a/one.go", LineNumber: 1, Match: "match1", Context: "> 1: match1"},
		{FilePath: "b/two.go", LineNumber: 5, Match: "match2", Context: "> 5: match2"},
		{FilePath: "b/two.go", LineNumber: 9, Match: "match3", Context: "> 9: match3"},
	}

	result := formatGrepResults(matches, false)

	if !strings.Contains(result, "3 matches in 2 files") {
		t.Errorf("expected summary '3 matches in 2 files', got:\n%s", result)
	}

	if !strings.Contains(result, "a/one.go (1 match):") {
		t.Errorf("expected singular 'match' for single result, got:\n%s", result)
	}

	if !strings.Contains(result, "b/two.go (2 matches):") {
		t.Errorf("expected '2 matches' for two.go, got:\n%s", result)
	}

	// File path for b/two.go appears exactly once (as header, not per-match)
	count := strings.Count(result, "b/two.go")
	if count != 1 {
		t.Errorf("expected b/two.go to appear once, got %d times", count)
	}
}

func TestFormatGrepResults_Truncated(t *testing.T) {
	matches := []GrepMatch{
		{FilePath: "x.go", LineNumber: 1, Match: "foo", Context: "> 1: foo"},
	}

	result := formatGrepResults(matches, true)

	if !strings.Contains(result, "[Results truncated at limit]") {
		t.Errorf("expected truncation notice, got:\n%s", result)
	}
}

func TestFormatGrepResults_Empty(t *testing.T) {
	result := formatGrepResults(nil, false)
	if result != "" {
		t.Errorf("expected empty string for nil matches, got %q", result)
	}
}

func TestGroupMatchesByFile_PreservesOrder(t *testing.T) {
	matches := []GrepMatch{
		{FilePath: "c.go", LineNumber: 1},
		{FilePath: "a.go", LineNumber: 1},
		{FilePath: "c.go", LineNumber: 2},
		{FilePath: "b.go", LineNumber: 1},
		{FilePath: "a.go", LineNumber: 2},
	}

	groups := groupMatchesByFile(matches)

	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	// Encounter order: c.go first, then a.go, then b.go
	if groups[0].path != "c.go" || groups[1].path != "a.go" || groups[2].path != "b.go" {
		t.Errorf("unexpected group order: %v, %v, %v", groups[0].path, groups[1].path, groups[2].path)
	}
	if len(groups[0].matches) != 2 || len(groups[1].matches) != 2 || len(groups[2].matches) != 1 {
		t.Errorf("unexpected match counts: %d, %d, %d", len(groups[0].matches), len(groups[1].matches), len(groups[2].matches))
	}
}

func TestGrepTool_GroupedOutput(t *testing.T) {
	dir := t.TempDir()

	// Write two files, both containing the token
	if err := os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("line1\nmatch_here\nline3"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.go"), []byte("other\nmatch_here\nend"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil, DefaultOutputLimits())
	args, _ := json.Marshal(GrepArgs{
		Pattern:      "match_here",
		Path:         dir,
		ContextLines: 0,
	})

	output, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	// Should have summary
	if !strings.Contains(output.Content, "2 matches in 2 files") {
		t.Errorf("expected grouped summary, got:\n%s", output.Content)
	}

	// Each file path should appear exactly once
	alphaPath := filepath.Join(dir, "alpha.go")
	betaPath := filepath.Join(dir, "beta.go")
	if strings.Count(output.Content, alphaPath) != 1 {
		t.Errorf("alpha.go should appear once, got:\n%s", output.Content)
	}
	if strings.Count(output.Content, betaPath) != 1 {
		t.Errorf("beta.go should appear once, got:\n%s", output.Content)
	}
}
