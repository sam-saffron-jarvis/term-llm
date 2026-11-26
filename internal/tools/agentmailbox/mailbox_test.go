package agentmailbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
)

type capableRunner struct{}

func (capableRunner) SupportsAgentMailbox() bool { return true }

func testInbox(t *testing.T, parent, id, address string, generation int64) (context.Context, *Inbox) {
	t.Helper()
	ctx, err := Admit(t.Context(), capableRunner{}, parent, id, "developer", generation, &address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(ctx) })
	return ctx, ForChild(ctx, id, parent)
}

func TestMailboxLifetimeAndStamp(t *testing.T) {
	t.Setenv(enabledEnv, "1")
	aliceCtx, alice := testInbox(t, "parent", "a", "alice", 1)
	bobCtx, bob := testInbox(t, "parent", "b", "bob", 1)
	_, other := testInbox(t, "other-parent", "c", "carol", 1)
	send := Tool{Name: SendToolName, Inbox: alice}
	wait := Tool{Name: WaitToolName, Inbox: bob}
	call := func(tool Tool, id, raw string) core.ToolOutput {
		t.Helper()
		out, err := tool.Execute(core.ContextWithSessionID(t.Context(), id), json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, attempt := range []struct{ id, address string }{{"parent", "bob"}, {"a", "alice"}, {"a", "carol"}, {"a", "missing"}} {
		if out := call(send, attempt.id, `{"to":"`+attempt.address+`","text":"not queued"}`); !out.IsError {
			t.Fatalf("unexpected queue acceptance for %+v: %s", attempt, out.Content)
		}
	}
	if m, _, _ := other.poll(t.Context()); m != nil {
		t.Fatal("sibling of a different parent received mail")
	}
	if out := call(send, "a", `{"to":"bob","text":"hello","from":"impostor"}`); out.IsError || out.Content != `{"status":"queued","to":"bob"}` {
		t.Fatalf("send result: %+v", out)
	}
	Close(aliceCtx)
	_, replacementSender := testInbox(t, "parent", "a", "alice", 2)
	const poll = `{"timeout_seconds":0}`
	out := call(wait, "b", poll)
	var result struct {
		Source, Status string
		Message        message
	}
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil || out.IsError || result.Status != "message" || result.Source != "peer_agent" || result.Message.From != "alice" || result.Message.ID != "a" || result.Message.Generation != 1 || result.Message.Text != "hello" || result.Message.SentAt.IsZero() {
		t.Fatalf("expected original sender stamp after completion and reuse, got %+v (%v)", out, err)
	}
	if err := replacementSender.send(t.Context(), "bob", "old inbox only"); err != nil {
		t.Fatal(err)
	}
	Close(bobCtx)
	if err := replacementSender.send(t.Context(), "bob", "too late"); err == nil {
		t.Fatal("accepted mail for a finished recipient")
	}
	_, replacement := testInbox(t, "parent", "b", "bob", 2)
	Close(bobCtx) // A stale cleanup cannot affect a reused address.
	if out := call(wait, "b", poll); !out.IsError {
		t.Fatalf("stale receiver still polled: %+v", out)
	}
	if out := call(Tool{Name: WaitToolName, Inbox: replacement}, "b", poll); out.IsError || out.Content != `{"status":"timeout"}` {
		t.Fatalf("new generation should have an empty inbox: %+v", out)
	}
	if err := replacementSender.send(t.Context(), "bob", "fresh"); err != nil {
		t.Fatalf("new recipient did not accept fresh mail: %v", err)
	}
}

func TestMailboxWaitBoundsAndCancellation(t *testing.T) {
	t.Setenv(enabledEnv, "1")
	_, alice := testInbox(t, "parent", "a", "alice", 1)
	bobCtx, bob := testInbox(t, "parent", "b", "bob", 1)
	sender := Tool{Name: SendToolName, Inbox: alice}
	if out, _ := sender.Execute(core.ContextWithSessionID(t.Context(), "a"), json.RawMessage(`{"to":"bob","text":"`+strings.Repeat("x", maxTextBytes+1)+`"}`)); !out.IsError {
		t.Fatalf("accepted oversized text: %+v", out)
	}
	waiter := Tool{Name: WaitToolName, Inbox: bob}
	for _, raw := range []string{`{}`, `{"timeout_seconds":-1}`, `{"timeout_seconds":301}`} {
		if out, _ := waiter.Execute(core.ContextWithSessionID(t.Context(), "b"), json.RawMessage(raw)); !out.IsError {
			t.Fatalf("accepted invalid wait %s: %+v", raw, out)
		}
	}
	for range maxQueued {
		if err := alice.send(t.Context(), "bob", "full soon"); err != nil {
			t.Fatal(err)
		}
	}
	if err := alice.send(t.Context(), "bob", "overflow"); err == nil {
		t.Fatal("accepted message beyond queue limit")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if m, err := bob.wait(ctx, 0); m != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled poll consumed mail: %v, %v", m, err)
	}
	if m, err := bob.wait(t.Context(), 0); m == nil || err != nil {
		t.Fatalf("message lost to cancelled poll: %v, %v", m, err)
	}
	if m, err := alice.wait(t.Context(), time.Millisecond); m != nil || err != nil {
		t.Fatalf("empty wait did not time out: %v, %v", m, err)
	}
	// A code-mode wait's park hook synchronizes with the blocked call, so
	// cancellation is tested without sleeping or racing its entry.
	parked := make(chan struct{})
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	ctx = core.ContextWithCodeModeToolPause(ctx, func() (func() error, error) {
		close(parked)
		return func() error { return nil }, nil
	})
	finished := make(chan error, 1)
	go func() { _, err := alice.wait(ctx, 30*time.Second); finished <- err }()
	select {
	case <-parked:
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not park")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait returned %v instead of cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not cancel")
	}
	Close(bobCtx)
}
