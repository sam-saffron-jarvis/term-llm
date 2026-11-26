package serve

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	runpkg "github.com/term-llm/term-llm/internal/run"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/tools/agentmailbox"
)

func TestServeChildMailboxToolResults(t *testing.T) {
	const optIn = "TERM_LLM_EXPERIMENTAL_AGENT_MAILBOX"
	t.Setenv(optIn, "1")
	cfg := &config.Config{DefaultProvider: "mock", Providers: map[string]config.ProviderConfig{"mock": {Model: "mock-model"}}}
	parent, alice, bob := session.NewID(), session.NewID(), session.NewID()
	aliceAddr, bobAddr := "alice", "bob"
	aliceCtx, err := agentmailbox.Admit(t.Context(), &SpawnAgentRunner{}, parent, alice, "developer", 1, &aliceAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentmailbox.Close(aliceCtx) })
	bobCtx, err := agentmailbox.Admit(t.Context(), &SpawnAgentRunner{}, parent, bob, "developer", 1, &bobAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentmailbox.Close(bobCtx) })

	for _, tc := range []struct {
		name                     string
		id                       string
		child, admitted, enabled bool
		tool                     string
		args                     map[string]any
		want                     string
	}{
		{name: "parent", id: alice, admitted: true, enabled: true},
		{name: "host disabled", id: alice, child: true, admitted: true},
		{name: "no admission", id: alice, child: true, enabled: true},
		{name: "wrong child", id: session.NewID(), child: true, admitted: true, enabled: true},
		{name: "send", id: alice, child: true, admitted: true, enabled: true, tool: agentmailbox.SendToolName, args: map[string]any{"to": "bob", "text": "parser is green"}, want: `"status":"queued"`},
		{name: "receive after sender exits", id: bob, child: true, admitted: true, enabled: true, tool: agentmailbox.WaitToolName, args: map[string]any{"timeout_seconds": 0}, want: `"from":"alice"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.enabled {
				t.Setenv(optIn, "")
			}
			ctx := t.Context()
			if tc.admitted {
				ctx = aliceCtx
				if tc.id == bob {
					ctx = bobCtx
				}
			}
			provider := mockprovider.NewMockProvider("mock")
			if tc.tool != "" {
				provider.AddToolCall("mail", tc.tool, tc.args)
			}
			provider.AddTextResponse("done")
			zero := 0
			req := runpkg.Request{Platform: runpkg.PlatformConsole, Prompt: "do delegated work", SessionID: tc.id, ParentSessionID: parent,
				IsSubagent: tc.child, Cwd: t.TempDir(), ProviderInstance: provider, RemainingDepth: &zero, MaxTurns: 3, MaxTurnsSet: true,
				OnEngineReady: func(eng *engine.Engine) {
					for _, name := range []string{agentmailbox.SendToolName, agentmailbox.WaitToolName} {
						found := false
						for _, spec := range eng.Tools().AllSpecs() {
							found = found || spec.Name == name
						}
						if found != (tc.tool != "") {
							t.Errorf("tool %s exposed = %t, expected %t", name, found, tc.tool != "")
						}
					}
				},
			}
			if _, err := newCmdRunner(cfg, cmdRunnerOptions{}).Run(ctx, req, eventSinkFunc(func(core.Event) {})); err != nil {
				t.Fatal(err)
			}
			if tc.tool == agentmailbox.SendToolName {
				agentmailbox.Close(aliceCtx)
			}
			if tc.tool == "" {
				return
			}
			for _, request := range provider.RecordedRequests() {
				for _, msg := range request.Messages {
					for _, part := range msg.Parts {
						if result := part.ToolResult; result != nil && result.ID == "mail" && !result.IsError && strings.Contains(result.Content, tc.want) && msg.Role == core.RoleTool {
							return
						}
					}
				}
			}
			t.Fatalf("no tool-role result containing %s", tc.want)
		})
	}
}
