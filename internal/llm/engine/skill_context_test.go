package engine_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/skills"
	"github.com/term-llm/term-llm/internal/tools"
)

func TestInlineSkillEngineRequestsRetainInstructionsAndExposeCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name         string
		introduction int
	}{
		{"first_reference_turn1", 0},
		{"first_reference_turn2", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".skills", "probe")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			const body = "Use the fixture; keep $ARGUMENTS[0] literal."
			manifest := "---\nname: probe\ndescription: Inline fixture\ncontext: fork\nallowed-tools: []\ntools:\n  - name: probe_script\n    description: Fixture script\n    script: probe.sh\n---\n" + body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			registry, err := skills.NewRegistry(skills.RegistryConfig{IncludeProjectSkills: true, ProjectDir: root})
			if err != nil {
				t.Fatal(err)
			}
			manager, err := tools.NewToolManager(&tools.ToolConfig{}, &config.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.ApprovalMgr.Close)
			// Exercise the request path used by native, session-owning CLI providers.
			provider := mockprovider.NewMockProvider("mock").WithCapabilities(core.Capabilities{
				ToolCalls: true, ManagesOwnContext: true, InlineToolLoop: true, OrderedInlineToolEvents: true,
			}).AddTextResponse("First.").AddTextResponse("Second.").AddTextResponse("Third.")
			eng := engine.NewEngine(provider, nil)
			tool := tools.NewActivateSkillTool(registry, nil)
			registrations, policyChanges := 0, 0
			tool.SetOnToolsActivated(func(defs []skills.SkillToolDef, dir string) {
				registrations++
				if err := tools.RegisterSkillScriptTools(eng, manager, defs, dir); err != nil {
					t.Fatal(err)
				}
			})
			tool.SetOnActivated(func([]string, bool) { policyChanges++ })
			if err := eng.Tools().Register(tool); err != nil {
				t.Fatal(err)
			}
			eng.SetAllowedToolsFilter([]string{tools.ActivateSkillToolName, "probe_script"})
			texts := []string{"Start this task.", "Continue this task.", "Continue without another reference."}
			texts[tc.introduction] = "Use /probe for this task."
			texts[tc.introduction+1] = "Again use /probe."
			history := []core.Message{core.SystemText("Be helpful.")}
			for i, text := range texts {
				history = append(history, core.UserText(text))
				stream, err := eng.Stream(context.Background(), core.Request{
					SessionID: "one", Messages: append([]core.Message(nil), history...), Tools: eng.Tools().AllSpecs(), MaxTurns: 2,
				})
				if err != nil {
					t.Fatal(err)
				}
				var reply strings.Builder
				for {
					event, recvErr := stream.Recv()
					if errors.Is(recvErr, io.EOF) {
						break
					}
					if recvErr != nil {
						t.Fatal(recvErr)
					}
					if event.Type == core.EventTextDelta {
						reply.WriteString(event.Text)
					}
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if reply.Len() == 0 {
					t.Fatal("missing assistant response for durable history")
				}
				// Persist only user/assistant history, never request-only skill context.
				history = append(history, core.AssistantText(reply.String()))
				if i == tc.introduction {
					if registrations != 1 {
						t.Fatalf("introduction registered scripts %d times, want once", registrations)
					}
					// Subsequent requests must restore cached instructions without the source.
					if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
						t.Fatal(err)
					}
				}
			}
			requests := provider.RecordedRequests()
			if len(requests) != 3 || registrations != 1 || policyChanges != 0 {
				t.Fatalf("requests=%d registrations=%d policy=%d", len(requests), registrations, policyChanges)
			}
			for i, request := range requests {
				if i > 0 {
					previous := requests[i-1].Messages
					growth := 2
					if i == tc.introduction {
						growth++ // Newly admitted instructions follow the already delivered prefix.
					}
					if len(request.Messages) != len(previous)+growth || !reflect.DeepEqual(request.Messages[:len(previous)], previous) {
						t.Fatalf("request %d did not preserve the prior provider-visible prefix:\nprevious: %#v\ncurrent: %#v", i+1, previous, request.Messages)
					}
				}
				var transcript strings.Builder
				for _, message := range request.Messages {
					transcript.WriteString(core.MessageText(message))
				}
				wantCount := 0
				if i >= tc.introduction {
					wantCount = 1
				}
				if got := strings.Count(transcript.String(), body); got != wantCount {
					t.Fatalf("request %d instruction count = %d, want %d", i+1, got, wantCount)
				}
				found := false
				for _, spec := range request.Tools {
					found = found || spec.Name == "probe_script"
				}
				if found != (i >= tc.introduction) {
					t.Fatalf("request %d preloaded capability present = %v, want %v", i+1, found, i >= tc.introduction)
				}
			}
		})
	}
}
