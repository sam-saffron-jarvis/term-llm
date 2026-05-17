package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestContextFetchToolStrictSchemaAndFailures(t *testing.T) {
	tool := &contextFetchTool{chunks: []contextChunk{{ID: "c1", SessionNumber: 1, MessageID: 2, Sequence: 3, Role: string(llm.RoleAssistant), Text: "alpha beta gamma"}}, limit: 5}
	spec := tool.Spec()
	if !spec.Strict {
		t.Fatal("context_fetch should use strict schema")
	}
	if got := spec.Schema["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %v, want false", got)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"","k":1}`)); err == nil {
		t.Fatal("empty query should fail closed")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"alpha","k":99}`)); err == nil {
		t.Fatal("out-of-range k should fail closed")
	}
}

func TestRankChunksFallsBackToRecentContext(t *testing.T) {
	chunks := []contextChunk{
		{ID: "old", Sequence: 1, Text: "alpha"},
		{ID: "new", Sequence: 2, Text: "beta"},
	}
	got := rankChunks("no lexical overlap", chunks, 1)
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("rankChunks fallback = %#v, want newest chunk", got)
	}
}

func TestEvidenceRecall(t *testing.T) {
	score := evidenceRecall("Fixed auth callback and restarted webui", []contextChunk{{Text: "auth callback failed before webui restart"}})
	if score <= 0.4 {
		t.Fatalf("evidenceRecall = %v, want useful lexical overlap", score)
	}
}
