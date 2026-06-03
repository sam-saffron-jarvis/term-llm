package cmd

import (
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestAskTurnMessagesAfterResponseCaptureSkipsLeadingAssistant(t *testing.T) {
	turnMessages := []llm.Message{
		llm.AssistantText("draft"),
		{Role: llm.RoleTool, Parts: []llm.Part{{Type: llm.PartText, Text: "tool result"}}},
	}

	got := askTurnMessagesAfterResponseCapture(true, turnMessages)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Role != llm.RoleTool {
		t.Fatalf("got[0].Role = %q, want %q", got[0].Role, llm.RoleTool)
	}
}

func TestAskTurnMessagesAfterResponseCaptureKeepsToolOnlyTurn(t *testing.T) {
	turnMessages := []llm.Message{
		{Role: llm.RoleTool, Parts: []llm.Part{{Type: llm.PartText, Text: "tool result"}}},
	}

	got := askTurnMessagesAfterResponseCapture(true, turnMessages)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Role != llm.RoleTool {
		t.Fatalf("got[0].Role = %q, want %q", got[0].Role, llm.RoleTool)
	}
}

func TestAskTurnMessagesAfterResponseCaptureKeepsAssistantWithoutResponseCallback(t *testing.T) {
	turnMessages := []llm.Message{
		llm.AssistantText("final answer"),
	}

	got := askTurnMessagesAfterResponseCapture(false, turnMessages)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Role != llm.RoleAssistant {
		t.Fatalf("got[0].Role = %q, want %q", got[0].Role, llm.RoleAssistant)
	}
}
