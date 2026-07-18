package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
)

func TestSkillActivationProvenanceRoundTripsAndStaysOutOfProviderParts(t *testing.T) {
	store, err := NewSQLiteStore(Config{Enabled: true, Path: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	sess := &Session{ID: "skill-session", Provider: "mock", Model: "mock", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	message := &Message{
		SessionID: sess.ID,
		Role:      llm.RoleDeveloper,
		Parts: []llm.Part{
			{Type: llm.PartSkillActivation, SkillActivation: &llm.SkillActivationProvenance{
				Name:           "review",
				SourcePath:     "/repo/.skills/review",
				Origin:         "user",
				Execution:      "isolated",
				RawArguments:   "internal/config",
				RunID:          "skill-1",
				ChildSessionID: "child-1",
			}},
			{Type: llm.PartText, Text: "historical expanded instructions"},
		},
		TextContent: "historical expanded instructions",
		CreatedAt:   time.Now(),
		Sequence:    -1,
	}
	if err := store.AddMessage(ctx, sess.ID, message); err != nil {
		t.Fatal(err)
	}

	messages, err := store.GetMessages(ctx, sess.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Parts) != 2 {
		t.Fatalf("stored messages = %#v", messages)
	}
	provenance := messages[0].Parts[0].SkillActivation
	if provenance == nil || provenance.Name != "review" || provenance.SourcePath != "/repo/.skills/review" || provenance.ChildSessionID != "child-1" {
		t.Fatalf("round-tripped provenance = %#v", provenance)
	}
	providerMessage := messages[0].ToLLMMessage()
	if len(providerMessage.Parts) != 1 || providerMessage.Parts[0].Type != llm.PartText || providerMessage.Parts[0].Text != "historical expanded instructions" {
		t.Fatalf("provider message parts = %#v, want only instruction text", providerMessage.Parts)
	}
}

func TestLLMActiveMessagesKeepsStoredSystemBeforeRetainedSkillContext(t *testing.T) {
	provenance := &llm.SkillActivationProvenance{Name: "review", SourcePath: "/repo/.skills/review", Origin: "user", Execution: "main"}
	messages := []Message{
		{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartSkillActivation, SkillActivation: provenance}, {Type: llm.PartText, Text: "Retained skill instructions"}}, TextContent: "Retained skill instructions"},
		{Role: llm.RoleSystem, Parts: []llm.Part{{Type: llm.PartText, Text: "Stored system"}}, TextContent: "Stored system"},
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "summary"}}, TextContent: "summary"},
	}

	active := LLMActiveMessages(messages, 1, "fallback system")
	if len(active) < 3 || active[0].Role != llm.RoleSystem || llm.MessageText(active[0]) != "Stored system" || active[1].Role != llm.RoleDeveloper {
		t.Fatalf("system/retained ordering = %#v", active)
	}
}

func TestLLMActiveMessagesKeepsOnlyLatestCompactedSkillContext(t *testing.T) {
	old := &llm.SkillActivationProvenance{Name: "review", SourcePath: "/repo/.skills/review", Origin: "user", Execution: "main", ActivatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano), RunID: "old"}
	latest := &llm.SkillActivationProvenance{Name: "review", SourcePath: "/repo/.skills/review", Origin: "user", Execution: "main", ActivatedAt: time.Now().Format(time.RFC3339Nano), RunID: "latest"}
	other := &llm.SkillActivationProvenance{Name: "explain", SourcePath: "/repo/.skills/explain", Origin: "user", Execution: "main", ActivatedAt: time.Now().Format(time.RFC3339Nano)}
	messages := []Message{
		{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartSkillActivation, SkillActivation: old}, {Type: llm.PartText, Text: "Old review instructions"}}, TextContent: "Old review instructions"},
		{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartText, Text: "old reply"}}},
		{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartSkillActivation, SkillActivation: latest}, {Type: llm.PartText, Text: "Latest review instructions"}}, TextContent: "Latest review instructions"},
		{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartSkillActivation, SkillActivation: other}, {Type: llm.PartText, Text: "Explain instructions"}}, TextContent: "Explain instructions"},
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "compacted summary"}}},
	}

	active := LLMActiveMessages(messages, 4, "system")
	var oldCount, latestCount, otherCount int
	for _, message := range active {
		text := llm.MessageText(message)
		if strings.Contains(text, "Old review instructions") {
			oldCount++
		}
		if strings.Contains(text, "Latest review instructions") {
			latestCount++
		}
		if strings.Contains(text, "Explain instructions") {
			otherCount++
		}
	}
	if oldCount != 0 || latestCount != 1 || otherCount != 1 {
		t.Fatalf("retained skill contexts old/latest/other = %d/%d/%d; messages=%#v", oldCount, latestCount, otherCount, active)
	}
}

func TestLLMActiveMessagesRetainsSkillInstructionsAcrossCompactionOnce(t *testing.T) {
	provenance := &llm.SkillActivationProvenance{Name: "review", Origin: "user", Execution: "main", ActivatedAt: time.Now().Format(time.RFC3339Nano)}
	messages := []Message{
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "old prompt"}}},
		{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartSkillActivation, SkillActivation: provenance}, {Type: llm.PartText, Text: "Pinned review instructions"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartText, Text: "old reply"}}},
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartText, Text: "compacted summary"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartText, Text: "ack"}}},
	}

	active := LLMActiveMessages(messages, 3, "system")
	var count int
	for _, message := range active {
		if strings.Contains(llm.MessageText(message), "Pinned review instructions") {
			count++
			if message.Role != llm.RoleDeveloper {
				t.Fatalf("retained skill role = %q", message.Role)
			}
		}
		for _, part := range message.Parts {
			if part.Type == llm.PartSkillActivation {
				t.Fatal("provider context leaked provenance control part")
			}
		}
	}
	if count != 1 {
		t.Fatalf("retained skill instruction count = %d, messages=%#v", count, active)
	}
}
