package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/skills"
)

const inlineSkillManifest = `---
name: computer-use
description: Inline fixture
context: fork
allowed-tools: []
code-mode:
  module: js/index.mjs
  files: [js/index.mjs]
---
Computer instructions; keep $ARGUMENTS[0] literal.
`

func inlineSkillTool(t *testing.T) *ActivateSkillTool {
	t.Helper()
	return NewActivateSkillTool(toolTestSkillRegistry(t, map[string]string{
		"computer-use": inlineSkillManifest,
	}), nil)
}

func inlineContext(t *testing.T, tool *ActivateSkillTool, sessionID string, messages []core.Message) []core.Message {
	t.Helper()
	got, err := tool.PrepareRequestContext(context.Background(), sessionID, messages)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func inlineBodyCount(messages []core.Message) int {
	count := 0
	for _, message := range messages {
		count += strings.Count(core.MessageText(message)+core.CollectToolResultText(message.Parts), "Computer instructions;")
	}
	return count
}

func TestInlineSkillPreloadRestoresContextWithoutRepeatedAdmission(t *testing.T) {
	tool := inlineSkillTool(t)
	admissions, policyChanges := 0, 0
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error { admissions++; return nil })
	tool.SetOnActivated(func([]string, bool) { policyChanges++ })
	original := []core.Message{core.UserText("Take a screenshot using /computer-use, then /computer-use again.")}
	before := append([]core.Message(nil), original...)
	first := inlineContext(t, tool, "one", original)
	if admissions != 1 || policyChanges != 0 || inlineBodyCount(first) != 1 {
		t.Fatalf("first preload: admissions=%d policy=%d messages=%+v", admissions, policyChanges, first)
	}
	if !reflect.DeepEqual(original, before) || !reflect.DeepEqual(first[1:], original) {
		t.Fatal("preload changed the original user message")
	}
	if first[0].Role != core.RoleDeveloper {
		t.Fatal("inline instructions manufactured another user task")
	}
	if !strings.Contains(core.MessageText(first[0]), "$ARGUMENTS[0]") ||
		strings.Contains(core.MessageText(first[0]), "Invocation arguments") {
		t.Fatal("inline preload expanded arguments or invoked a fork skill")
	}
	// The request-only body is not in durable history: restore it even when
	// the next user message contains no reference at all.
	second := inlineContext(t, tool, "one", []core.Message{core.UserText("Continue.")})
	if admissions != 1 || inlineBodyCount(second) != 1 {
		t.Fatalf("lost cached instructions: admissions=%d messages=%+v", admissions, second)
	}
	repeated := inlineContext(t, tool, "one", original)
	if admissions != 1 || inlineBodyCount(repeated) != 1 {
		t.Fatalf("repeated reference reactivated: admissions=%d messages=%+v", admissions, repeated)
	}
	// If the caller does retain the request-only message, don't duplicate it.
	retained := inlineContext(t, tool, "one", first)
	if inlineBodyCount(retained) != 1 || admissions != 1 {
		t.Fatal("duplicated retained instruction context")
	}
	other := inlineContext(t, tool, "two", []core.Message{core.UserText("Continue.")})
	if inlineBodyCount(other) != 0 {
		t.Fatal("instructions leaked into another session")
	}
	inlineContext(t, tool, "two", original)
	if admissions != 2 {
		t.Fatal("another session did not independently activate")
	}
}

func TestInlineSkillExplicitReloadAlwaysRunsAndMarksCache(t *testing.T) {
	tool := inlineSkillTool(t)
	admissions, policyChanges := 0, 0
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error { admissions++; return nil })
	tool.SetOnActivated(func([]string, bool) { policyChanges++ })
	ctx := core.ContextWithSessionID(context.Background(), "one")
	args := json.RawMessage(`{"name":"computer-use","prompt":"reload context"}`)
	for i := 0; i < 2; i++ {
		out, err := tool.Execute(ctx, args)
		if err != nil || !strings.Contains(out.Content, "**Task context:** reload context") {
			t.Fatalf("explicit activation: output=%+v err=%v", out, err)
		}
		messages := []core.Message{
			core.ToolResultMessage("activation", ActivateSkillToolName, out.Content, nil),
			core.UserText("Use /computer-use again."),
		}
		got := inlineContext(t, tool, "one", messages)
		if len(got) != len(messages) || inlineBodyCount(got) != 1 {
			t.Fatal("explicit tool response was duplicated by automatic loading")
		}
	}
	if admissions != 2 || policyChanges != 2 {
		t.Fatalf("explicit reload was suppressed: admissions=%d policy=%d", admissions, policyChanges)
	}
	inlineContext(t, tool, "one", []core.Message{core.UserText("Please use /computer-use.")})
	out, err := tool.Execute(ctx, args)
	if err != nil || !strings.Contains(out.Content, "Computer instructions;") || admissions != 3 {
		t.Fatalf("explicit reload after auto reference: output=%+v err=%v admissions=%d", out, err, admissions)
	}
}

func TestInlineSkillFailuresStayRetryable(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "auto"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			tool := inlineSkillTool(t)
			attempts := 0
			tool.SetOnCodeModuleActivated(func(*skills.Activation) error {
				attempts++
				if attempts == 1 {
					return errors.New("admission failed")
				}
				return nil
			})
			original := []core.Message{core.UserText("Use /computer-use.")}
			if explicit {
				_, err := tool.Execute(core.ContextWithSessionID(context.Background(), "one"), json.RawMessage(`{"name":"computer-use"}`))
				if err == nil {
					t.Fatal("explicit admission failure was swallowed")
				}
			} else if got := inlineContext(t, tool, "one", original); !reflect.DeepEqual(got, original) {
				t.Fatal("failed preload injected instructions")
			}
			got := inlineContext(t, tool, "one", original)
			if attempts != 2 || inlineBodyCount(got) != 1 {
				t.Fatalf("failure marked cache: attempts=%d messages=%+v", attempts, got)
			}
		})
	}
}

func TestInlineSkillCompactionAndSessionReset(t *testing.T) {
	tool := inlineSkillTool(t)
	admissions := 0
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error { admissions++; return nil })
	original := []core.Message{core.UserText("Use /computer-use.")}
	for _, sessionID := range []string{"one", "two", ""} {
		inlineContext(t, tool, sessionID, original)
	}
	eng := engine.NewEngine(mockprovider.NewMockProvider("mock"), nil)
	if err := eng.Tools().Register(tool); err != nil {
		t.Fatal(err)
	}
	if err := eng.PrepareCompactionContext(context.Background(), "one", eng.Tools().AllSpecs(), &engine.CompactionResult{}); err != nil {
		t.Fatal(err)
	}
	if got := inlineContext(t, tool, "one", []core.Message{core.UserText("Continue.")}); inlineBodyCount(got) != 0 {
		t.Fatal("compaction retained pre-boundary context")
	}
	inlineContext(t, tool, "one", original)
	inlineContext(t, tool, "two", original)
	if admissions != 4 {
		t.Fatalf("compaction reset wrong session: admissions=%d", admissions)
	}
	eng.ResetSessionState("two")
	eng.ResetSessionState("")
	inlineContext(t, tool, "two", original)
	inlineContext(t, tool, "", original)
	if admissions != 6 {
		t.Fatalf("out-of-band reset did not clear cache: admissions=%d", admissions)
	}
}

func TestInlineSkillPreloadBypassesInvocationGatesButNotLeadingCommands(t *testing.T) {
	tool := NewActivateSkillTool(toolTestSkillRegistry(t, map[string]string{
		"computer-use": strings.Replace(inlineSkillManifest, "context: fork", "context: fork\nuser-invocable: false\ndisable-model-invocation: true", 1),
	}), nil)
	admissions := 0
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error { admissions++; return nil })
	for _, text := range []string{"/computer-use args", "  /computer-use args", "Use /missing.", "See /computer-use/path", "Use x/computer-use"} {
		messages := []core.Message{core.UserText(text)}
		if got := inlineContext(t, tool, "one", messages); !reflect.DeepEqual(got, messages) {
			t.Fatalf("unexpected preload for %q", text)
		}
	}
	got := inlineContext(t, tool, "one", []core.Message{core.UserText("Use /computer-use.")})
	if admissions != 1 || inlineBodyCount(got) != 1 {
		t.Fatalf("explicit inline reference did not bypass invocation gates: %+v", got)
	}
}

func TestInlineSkillBlankSessionsDoNotInheritInstructions(t *testing.T) {
	for _, sessionID := range []string{"", " "} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("session=%q/explicit=%t", sessionID, explicit), func(t *testing.T) {
				tool := inlineSkillTool(t)
				tool.SetOnCodeModuleActivated(func(*skills.Activation) error { return nil })
				if explicit {
					_, err := tool.Execute(core.ContextWithSessionID(context.Background(), sessionID), json.RawMessage(`{"name":"computer-use"}`))
					if err != nil {
						t.Fatal(err)
					}
				} else {
					first := inlineContext(t, tool, sessionID, []core.Message{core.UserText("Please use /computer-use.")})
					if inlineBodyCount(first) != 1 {
						t.Fatal("one-shot request did not load its referenced skill")
					}
				}
				next := []core.Message{core.UserText("An unrelated one-shot task.")}
				if got := inlineContext(t, tool, sessionID, next); !reflect.DeepEqual(got, next) {
					t.Fatal("fresh one-shot request inherited another run's instructions")
				}
			})
		}
	}
}

func TestInlineSkillAnchorsToSuccessfulAdmissionTurn(t *testing.T) {
	tool := inlineSkillTool(t)
	attempts := 0
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporarily unavailable")
		}
		return nil
	})
	const request = "Please use /computer-use."
	history := []core.Message{core.SystemText("system"), core.UserText(request)}
	failed := inlineContext(t, tool, "one", history)
	if !reflect.DeepEqual(failed, history) {
		t.Fatal("failed admission changed request context")
	}
	history = append(history, core.AssistantText("Unavailable."), core.UserText(request))
	first := inlineContext(t, tool, "one", history)
	if !reflect.DeepEqual(first[:len(failed)], failed) || first[len(first)-2].Role != core.RoleDeveloper {
		t.Fatal("retry inserted instructions before an already-delivered turn")
	}
	history = append(history, core.AssistantText("Done."), core.UserText("Continue."))
	next := inlineContext(t, tool, "one", history)
	if !reflect.DeepEqual(next[:len(first)], first) || inlineBodyCount(next) != 1 || attempts != 2 {
		t.Fatal("replayed instructions moved from their successful admission turn")
	}
}

func TestInlineSkillLaterAlphabeticalSkillPreservesDeliveredPrefix(t *testing.T) {
	tool := NewActivateSkillTool(toolTestSkillRegistry(t, map[string]string{
		"computer-use": inlineSkillManifest,
		"aaa-skill":    strings.ReplaceAll(inlineSkillManifest, "computer-use", "aaa-skill"),
	}), nil)
	tool.SetOnCodeModuleActivated(func(*skills.Activation) error { return nil })
	history := []core.Message{core.SystemText("system"), core.UserText("Use /computer-use.")}
	first := inlineContext(t, tool, "one", history)
	history = append(history, core.AssistantText("Done."), core.UserText("Now use /aaa-skill."))
	second := inlineContext(t, tool, "one", history)
	if !reflect.DeepEqual(second[:len(first)], first) || second[len(second)-2].Role != core.RoleDeveloper {
		t.Fatal("later alphabetical skill changed the already-delivered prefix")
	}
	history = append(history, core.AssistantText("Done again."), core.UserText("Continue."))
	third := inlineContext(t, tool, "one", history)
	if !reflect.DeepEqual(third[:len(second)], second) {
		t.Fatal("multiple cached skills moved on the next request")
	}
}
