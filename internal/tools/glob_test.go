package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToolFindsMatchesAndSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	visible := filepath.Join(dir, "visible.txt")
	hiddenDirFile := filepath.Join(dir, ".hidden", "secret.txt")
	hiddenFile := filepath.Join(dir, ".secret.txt")
	if err := os.WriteFile(visible, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(hiddenDirFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hiddenDirFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hiddenFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(nil)
	args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: "**/*.txt"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out.Content, visible) {
		t.Fatalf("expected visible file in output, got:\n%s", out.Content)
	}
	if strings.Contains(out.Content, hiddenDirFile) || strings.Contains(out.Content, hiddenFile) {
		t.Fatalf("hidden files should be skipped, got:\n%s", out.Content)
	}

	for _, pattern := range []string{".secret.txt", ".hidden/**"} {
		t.Run("explicit hidden "+pattern, func(t *testing.T) {
			args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: pattern})
			out, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if out.Content != "No files matched the pattern." {
				t.Fatalf("explicit hidden pattern should be ignored, got:\n%s", out.Content)
			}
		})
	}
}

func TestGlobToolDoesNotTraverseSymlinkDirectory(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	tool := NewGlobTool(nil)
	for _, pattern := range []string{"link/*.txt", "link/secret.txt"} {
		t.Run(pattern, func(t *testing.T) {
			args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: pattern})
			out, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if out.Content != "No files matched the pattern." {
				t.Fatalf("symlink directories must not be traversed, got:\n%s", out.Content)
			}
		})
	}
}

func TestGlobToolCancelledContext(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tool := NewGlobTool(nil)
	args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: "missing.txt"})
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out.Content, context.Canceled.Error()) {
		t.Fatalf("expected cancellation to be reported, got:\n%s", out.Content)
	}
}

func TestGlobToolTruncatesAtLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxGlobResults+5; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(nil)
	args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: "*.txt"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out.Content, fmt.Sprintf("[Results truncated at %d files]", maxGlobResults)) {
		t.Fatalf("expected truncation notice, got:\n%s", out.Content)
	}
}

func TestGlobToolRejectsEscapingPattern(t *testing.T) {
	tool := NewGlobTool(nil)
	for _, pattern := range []string{"../*.txt", "{..,safe}/*.txt"} {
		t.Run(pattern, func(t *testing.T) {
			args, _ := json.Marshal(GlobArgs{Path: t.TempDir(), Pattern: pattern})
			out, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if !strings.Contains(out.Content, "Error [INVALID_PARAMS]") {
				t.Fatalf("expected invalid pattern error, got:\n%s", out.Content)
			}
		})
	}
}

func TestGlobToolExactPattern(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(target, []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "go.mod"), []byte("module nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(nil)
	args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: "go.mod"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if !strings.Contains(out.Content, target) {
		t.Fatalf("expected root go.mod in output, got:\n%s", out.Content)
	}
	if strings.Contains(out.Content, filepath.Join(dir, "nested", "go.mod")) {
		t.Fatalf("exact pattern should not match nested go.mod, got:\n%s", out.Content)
	}
}

func BenchmarkGlobToolExactPatternLargeTree(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		b.Fatal(err)
	}

	for d := 0; d < 80; d++ {
		subdir := filepath.Join(dir, fmt.Sprintf("dir-%03d", d))
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < 80; f++ {
			path := filepath.Join(subdir, fmt.Sprintf("file-%03d.txt", f))
			if err := os.WriteFile(path, []byte("noise"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}

	tool := NewGlobTool(nil)
	args, _ := json.Marshal(GlobArgs{Path: dir, Pattern: "target.txt"})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := tool.Execute(context.Background(), args)
		if err != nil {
			b.Fatalf("Execute returned error: %v", err)
		}
		if !strings.Contains(out.Content, target) {
			b.Fatalf("glob output missing target: %s", out.Content)
		}
	}
}
