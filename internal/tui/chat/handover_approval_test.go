package chat

import (
	"strings"
	"testing"
)

func TestHandoverApprovalTranscriptIncludesOperatorIntent(t *testing.T) {
	entries := handoverApprovalTranscript("codebase", "reviewer", "focus on tests")
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].Role != "user" {
		t.Fatalf("role = %q, want user", entries[0].Role)
	}
	for _, want := range []string{"operator initiated a handover", "codebase", "reviewer", "focus on tests"} {
		if !strings.Contains(entries[0].Text, want) {
			t.Fatalf("handover evidence missing %q: %q", want, entries[0].Text)
		}
	}
}
