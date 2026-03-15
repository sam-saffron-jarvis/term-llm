package sessiontitle

import (
	"context"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
)

func TestParseCandidate(t *testing.T) {
	raw := "```json\n{\"short_title\":\"Fix cross-session bleed\",\"long_title\":\"Repairing web UI session switching and resume correctness\",\"confidence\":0.82}\n```"
	cand, err := ParseCandidate(raw)
	if err != nil {
		t.Fatalf("ParseCandidate: %v", err)
	}
	if cand.ShortTitle != "Fix cross-session bleed" {
		t.Fatalf("ShortTitle = %q", cand.ShortTitle)
	}
	if cand.LongTitle != "Repairing web UI session switching and resume correctness" {
		t.Fatalf("LongTitle = %q", cand.LongTitle)
	}
	if !Acceptable(cand) {
		t.Fatal("candidate should be acceptable")
	}
}

func TestBuildConversationSlice(t *testing.T) {
	messages := []session.Message{
		{Role: llm.RoleUser, TextContent: "<<<<< FILE: /tmp/task.txt >>>>> Work in the term-llm worktree and redesign the resume browser to use full width and show more context."},
		{Role: llm.RoleAssistant, TextContent: "I'll inspect the existing implementation and propose a cleaner browsing experience."},
		{Role: llm.RoleUser, TextContent: "It should feel delightful and not like a cramped dialog."},
	}
	slice := BuildConversationSlice(messages)
	if slice == "" {
		t.Fatal("BuildConversationSlice returned empty string")
	}
	if want := "User: Work in the term-llm worktree"; slice[:len(want)] != want {
		t.Fatalf("unexpected slice prefix: %q", slice)
	}
}

func TestGenerateUsesProvider(t *testing.T) {
	provider := llm.NewMockProvider("fast").AddTextResponse(`{"short_title":"Delightful resume browser redesign","long_title":"Redesigning chat TUI resume flow into a real session browser","confidence":0.91}`)
	sess := &session.Session{Summary: "resume command in chat tui is ugly"}
	messages := []session.Message{{Role: llm.RoleUser, TextContent: "resume command in chat tui is ugly and cramped"}}
	cand, err := Generate(context.Background(), provider, sess, messages)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if cand.ShortTitle != "Delightful resume browser redesign" {
		t.Fatalf("ShortTitle = %q", cand.ShortTitle)
	}
}
