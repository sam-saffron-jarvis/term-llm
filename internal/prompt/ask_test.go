package prompt

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/input"
)

func TestAskUserPrompt(t *testing.T) {
	t.Run("basic question without files", func(t *testing.T) {
		result := AskUserPrompt("What is Go?", nil, "")
		expected := "What is Go?"
		if result != expected {
			t.Errorf("expected: %s\ngot: %s", expected, result)
		}
	})

	t.Run("question with single file", func(t *testing.T) {
		files := []input.FileContent{
			{Path: "main.go", Content: "package main\n\nfunc main() {}"},
		}
		result := AskUserPrompt("Explain this code", files, "")
		if !strings.Contains(result, `<file path="main.go">`) {
			t.Error("missing file tag")
		}
		if !strings.Contains(result, "package main") {
			t.Error("missing file content")
		}
		if !strings.Contains(result, "Explain this code") {
			t.Error("missing question")
		}
	})

	t.Run("question with multiple files", func(t *testing.T) {
		files := []input.FileContent{
			{Path: "a.txt", Content: "aaa"},
			{Path: "b.txt", Content: "bbb"},
		}
		result := AskUserPrompt("Compare these files", files, "")
		if !strings.Contains(result, `<file path="a.txt">`) {
			t.Error("missing first file tag")
		}
		if !strings.Contains(result, `<file path="b.txt">`) {
			t.Error("missing second file tag")
		}
		if !strings.Contains(result, "Compare these files") {
			t.Error("missing question")
		}
	})

	t.Run("question with stdin", func(t *testing.T) {
		result := AskUserPrompt("What is this?", nil, "some piped content")
		if !strings.Contains(result, "<stdin>") {
			t.Error("missing stdin tag")
		}
		if !strings.Contains(result, "some piped content") {
			t.Error("missing stdin content")
		}
		if !strings.Contains(result, "What is this?") {
			t.Error("missing question")
		}
	})

	t.Run("question with files and stdin", func(t *testing.T) {
		files := []input.FileContent{
			{Path: "code.py", Content: "print('hello')"},
		}
		result := AskUserPrompt("Analyze this", files, "context info")
		if !strings.Contains(result, `<file path="code.py">`) {
			t.Error("missing file tag")
		}
		if !strings.Contains(result, "<stdin>") {
			t.Error("missing stdin tag")
		}
		if !strings.Contains(result, "Analyze this") {
			t.Error("missing question")
		}
	})

	t.Run("file content order", func(t *testing.T) {
		files := []input.FileContent{
			{Path: "first.txt", Content: "first"},
		}
		result := AskUserPrompt("question", files, "")
		// File content should come before the question
		fileIdx := strings.Index(result, "<file")
		questionIdx := strings.Index(result, "question")
		if fileIdx > questionIdx {
			t.Error("file content should come before the question")
		}
	})
}

func TestAskSystemPrompt(t *testing.T) {
	t.Run("empty instructions", func(t *testing.T) {
		result := AskSystemPrompt("")
		if result != "" {
			t.Errorf("expected empty string, got: %s", result)
		}
	})

	t.Run("with instructions", func(t *testing.T) {
		instructions := "Be concise and technical"
		result := AskSystemPrompt(instructions)
		if result != instructions {
			t.Errorf("expected: %s\ngot: %s", instructions, result)
		}
	})
}
