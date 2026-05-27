package llm

import (
	"strings"
	"testing"
)

func TestFormatEmbeddedFileTextUsesExplicitMarkers(t *testing.T) {
	got := FormatEmbeddedFileText("/tmp/report.csv", "text/csv", "name,count\napples,3\n")

	if !strings.Contains(got, "--- BEGIN USER-PROVIDED FILE: report.csv (text/csv) ---") {
		t.Fatalf("missing begin marker: %q", got)
	}
	if !strings.Contains(got, "```csv\nname,count") {
		t.Fatalf("missing csv fenced content: %q", got)
	}
	if !strings.Contains(got, "--- END USER-PROVIDED FILE: report.csv ---") {
		t.Fatalf("missing end marker: %q", got)
	}
	if strings.Contains(got, "/tmp/") {
		t.Fatalf("marker leaked path: %q", got)
	}
}

func TestFormatEmbeddedFileTextExtendsFenceForMarkdownContent(t *testing.T) {
	got := FormatEmbeddedFileText("notes.md", "text/markdown", "before\n```\ninside\n```\nafter")

	if !strings.Contains(got, "````markdown\n") {
		t.Fatalf("expected extended opening fence for content containing backticks: %q", got)
	}
	if !strings.Contains(got, "\n````\n--- END USER-PROVIDED FILE: notes.md ---") {
		t.Fatalf("expected matching extended closing fence: %q", got)
	}
}

func TestEmbeddedFileDisplayNameSanitizesPathsAndControls(t *testing.T) {
	got := EmbeddedFileDisplayName("C:\\Users\\me\\bad\nname.csv")
	if got != "bad name.csv" {
		t.Fatalf("display name = %q, want %q", got, "bad name.csv")
	}
}

func TestStripEmbeddedFileText(t *testing.T) {
	content := "please summarize\n\n" + EmbeddedFileIntro + "\n\n" + FormatEmbeddedFileText("a.txt", "text/plain", "hello")
	if got := StripEmbeddedFileText(content); got != "please summarize" {
		t.Fatalf("StripEmbeddedFileText() = %q", got)
	}

	onlyFile := FormatEmbeddedFileText("a.txt", "text/plain", "hello")
	if got := StripEmbeddedFileText(onlyFile); got != "" {
		t.Fatalf("StripEmbeddedFileText(file-only) = %q, want empty display text", got)
	}
}

func TestExtractEmbeddedFileNames(t *testing.T) {
	content := FormatEmbeddedFileText("/tmp/a.txt", "text/plain", "a") + FormatEmbeddedFileText("report (draft).csv", "text/csv", "b")
	got := ExtractEmbeddedFileNames(content)
	want := []string{"a.txt", "report (draft).csv"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ExtractEmbeddedFileNames() = %#v, want %#v", got, want)
	}
}
