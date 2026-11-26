package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/tools/agentmailbox"
)

type mailboxRunner struct {
	*lifecycleRunner
	children chan context.Context
}

func (*mailboxRunner) SupportsAgentMailbox() bool { return true }

func (r *mailboxRunner) RunAgentWithCallbackAndOptions(ctx context.Context, _, _ string, _ int, _ string, _ SubagentEventCallback, opts SpawnAgentRunOptions) (SpawnAgentRunResult, error) {
	r.children <- ctx
	select {
	case <-r.release:
		return SpawnAgentRunResult{SessionID: opts.ChildSessionID}, nil
	case <-ctx.Done():
		return SpawnAgentRunResult{}, ctx.Err()
	}
}

func (r *mailboxRunner) ContinueAgent(ctx context.Context, id, _, _ string, _ int, _ string, _ SpawnAgentRunOptions, _ SubagentEventCallback) (SpawnAgentRunResult, error) {
	r.children <- ctx
	return SpawnAgentRunResult{SessionID: id}, nil
}

func TestAgentMailboxAdmission(t *testing.T) {
	const optIn = "TERM_LLM_EXPERIMENTAL_AGENT_MAILBOX"
	t.Setenv(optIn, "")
	parent := session.NewID()
	ctx := core.ContextWithSessionID(context.Background(), parent)
	manager := newAgentBindingRegistry(t, SpawnAgentToolName, agentmailbox.SendToolName, agentmailbox.WaitToolName)
	spawn := manager.GetSpawnAgentTool()
	t.Cleanup(manager.ApprovalMgr.Close)
	t.Cleanup(func() { _ = spawn.Shutdown(t.Context()) })
	runner := &mailboxRunner{lifecycleRunner: &lifecycleRunner{release: make(chan struct{})}, children: make(chan context.Context, 4)}
	spawn.SetRunner(runner)
	const withAddress = `{"agent_name":"developer","prompt":"work","wait":0,"mailbox":"alice"}`
	reject := func(raw, reason string) {
		t.Helper()
		out := lifecycleCall(t, spawn, ctx, raw)
		if !out.IsError || !strings.Contains(out.Content, reason) {
			t.Fatalf("expected %q, got %+v", reason, out)
		}
	}
	reject(withAddress, "disabled")
	t.Setenv(optIn, "1")
	for _, name := range []string{agentmailbox.SendToolName, agentmailbox.WaitToolName} {
		if _, ok := manager.Registry.Get(name); ok || manager.Registry.IsEnabled(name) {
			t.Fatalf("explicit tool list exposed %s on the parent", name)
		}
	}
	spawn.SetRunner(runner.lifecycleRunner) // Supports run options, not mailbox admission.
	reject(withAddress, "does not support")
	spawn.SetRunner(runner)
	reject(strings.Replace(withAddress, `"alice"`, `""`, 1), "address matching")
	alice := lifecycleResult(t, lifecycleCall(t, spawn, ctx, withAddress))
	nextChild := func() context.Context {
		t.Helper()
		select {
		case child := <-runner.children:
			return child
		case <-time.After(3 * time.Second):
			t.Fatal("child did not start")
			return nil
		}
	}
	child := nextChild()
	inbox := agentmailbox.ForChild(child, alice.AgentID, parent)
	if inbox == nil || agentmailbox.ForChild(child, alice.AgentID, "wrong-parent") != nil {
		t.Fatal("expected an inbox bound to exactly the admitted child and parent")
	}
	reject(withAddress, "already in use")
	close(runner.release)
	control := func(name, args string) core.ToolOutput {
		t.Helper()
		tool, _ := manager.Registry.Get(name)
		return lifecycleCall(t, tool, ctx, args)
	}
	if out := control(WaitAgentToolName, fmt.Sprintf(`{"agent_ids":[%q],"max_wait":3}`, alice.AgentID)); out.IsError || !strings.Contains(out.Content, `"status":"completed"`) {
		t.Fatalf("wait_agent: %+v", out)
	}
	if out := control(ContinueAgentToolName, fmt.Sprintf(`{"agent_id":%q,"instructions":"continue","wait":0}`, alice.AgentID)); out.IsError {
		t.Fatalf("continue_agent: %+v", out)
	}
	if agentmailbox.ForChild(nextChild(), alice.AgentID, parent) != nil || inbox.Available() {
		t.Fatal("continuation inherited a mailbox or left its old generation live")
	}
}

func TestAgentMailboxCodeModeBinding(t *testing.T) {
	t.Setenv("TERM_LLM_EXPERIMENTAL_AGENT_MAILBOX", "1")
	parent := session.NewID()
	makeInbox := func(address string) *agentmailbox.Inbox {
		t.Helper()
		ctx, err := agentmailbox.Admit(t.Context(), &mailboxRunner{}, parent, address, "developer", 1, &address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { agentmailbox.Close(ctx) })
		return agentmailbox.ForChild(ctx, address, parent)
	}
	alice, bob := makeInbox("alice"), makeInbox("bob")
	cfg := DefaultToolConfig()
	cfg.Enabled = []string{agentmailbox.WaitToolName}
	cfg.Mailbox = bob
	manager, err := NewToolManager(&cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.ApprovalMgr.Close)
	authority := captureBindingAuthority(t, manager.Registry, "bob", cfg.Enabled)
	defer authority.Close()
	wait, ok := authority.Tool(agentmailbox.WaitToolName)
	if !ok {
		t.Fatal("wait_message was not bound to code mode")
	}
	send := agentmailbox.Tool{Name: agentmailbox.SendToolName, Inbox: alice}
	if got := lifecycleCall(t, send, core.ContextWithSessionID(t.Context(), "alice"), `{"to":"bob","text":"hi"}`); got.IsError {
		t.Fatal(got.Content)
	}
	if got := lifecycleCall(t, wait, core.ContextWithSessionID(t.Context(), "bob"), `{"timeout_seconds":0}`); got.IsError || !strings.Contains(got.Content, `"from":"alice"`) {
		t.Fatalf("bound wait: %+v", got)
	}
}
