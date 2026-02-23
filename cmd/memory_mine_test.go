package cmd

import (
	"testing"
)

func TestParseExtractionOperations_PlainJSON(t *testing.T) {
	raw := `{"operations": [{"op": "skip", "reason": "nothing to extract"}]}`
	ops, err := parseExtractionOperations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "skip" {
		t.Fatalf("unexpected ops: %+v", ops)
	}
}

func TestParseExtractionOperations_MarkdownFence(t *testing.T) {
	raw := "```json\n{\"operations\": [{\"op\": \"skip\", \"reason\": \"nothing to extract\"}]}\n```"
	ops, err := parseExtractionOperations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Op != "skip" {
		t.Fatalf("unexpected ops: %+v", ops)
	}
}

func TestParseExtractionOperations_MarkdownFenceNoLang(t *testing.T) {
	raw := "```\n{\"operations\": [{\"op\": \"skip\", \"reason\": \"nothing\"}]}\n```"
	ops, err := parseExtractionOperations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("unexpected ops: %+v", ops)
	}
}
