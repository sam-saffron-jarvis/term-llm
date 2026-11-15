package clibin

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// Importing tools here would cycle through tools -> llm -> clibin. The engine
// regression exercises real skill preloading; this test checks its anchored
// request shape against native Claude serialization and continuation.
func TestClaudeBinInlineSkillContextPreservesContinuation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		introduction int
	}{
		{"first_reference_turn1", 0},
		{"first_reference_turn2", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newClaudeContinuationHarness(t, "session-inline-skill")
			const body = "Use the fixture; keep $ARGUMENTS[0] literal."
			skill := core.Message{Role: core.RoleDeveloper, Parts: []core.Part{{Type: core.PartText, Text: body}}}
			history := []core.Message{core.SystemText("Be helpful.")}
			texts := []string{"Start this task.", "Continue this task.", "Continue without another reference."}
			texts[tc.introduction] = "Use /probe for this task."
			texts[tc.introduction+1] = "Again use /probe."
			for i, text := range texts {
				history = append(history, core.UserText(text))
				messages := append([]core.Message(nil), history...)
				if i >= tc.introduction {
					// Rebuild request-only instructions at the fixed introducing user
					// turn, never persisting them into durable user/assistant history.
					anchor := 1 + 2*tc.introduction
					messages = append(append(append([]core.Message(nil), history[:anchor]...), skill), history[anchor:]...)
				}
				h.turn(t, messages...)
				if got, want := h.resumed(), i > 0; got != want {
					t.Fatalf("request %d resumed = %v, want %v: %v", i+1, got, want, h.args)
				}
				input := h.inputText(t)
				if i == tc.introduction {
					if strings.Count(input, body) != 1 || !strings.Contains(input, text) {
						t.Fatalf("introducing stdin must contain skill instructions once and the user request: %q", input)
					}
				} else if input != text {
					t.Fatalf("request %d stdin = %q, want only new request %q", i+1, input, text)
				}
				if i != tc.introduction && strings.Contains(h.stdin, body) {
					t.Fatalf("request %d unexpectedly included skill instructions: %s", i+1, h.stdin)
				}
				for _, banned := range []string{"<conversation_history>", "ok"} {
					if strings.Contains(h.stdin, banned) {
						t.Fatalf("request %d replayed %q into native session: %s", i+1, banned, h.stdin)
					}
				}
				history = append(history, core.AssistantText("ok"))
			}
		})
	}
}
