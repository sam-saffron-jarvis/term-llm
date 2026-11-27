package session

import (
	"context"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// CLI providers (claude-bin) persist a turn as [assistant text + tool calls,
// tool, tool, ...]. Forking after that response must keep its tool results,
// which carry the media the response embeds.
func TestCreateBranchAfterCLIResponseCopiesTrailingToolResults(t *testing.T) {
	ctx := context.Background()
	store, source := newBranchTestStore(t)
	addBranchTestMessage(t, store, source.ID, core.UserText("make mocks"))
	answer := addBranchTestMessage(t, store, source.ID, core.AssistantText("here are the mocks"))
	addBranchTestMessage(t, store, source.ID, core.ToolResultMessage("call-1", "show_media", "ok", nil))
	lastTool := addBranchTestMessage(t, store, source.ID, core.ToolResultMessage("call-2", "show_media", "ok", nil))
	addBranchTestMessage(t, store, source.ID, core.UserText("pick A"))
	addBranchTestMessage(t, store, source.ID, core.AssistantText("building A"))

	result, err := store.CreateBranch(ctx, source.ID, CreateBranchOptions{AnchorMessageID: answer.ID, IdempotencyKey: "cli-turn"})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := store.GetMessages(ctx, result.Session.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 4 || copied[2].Role != core.RoleTool || copied[3].Role != core.RoleTool || result.AnchorMessageID != copied[1].ID {
		t.Fatalf("copied = %#v, result = %#v", copied, result)
	}
	if result.ForkAfterMessageID != answer.ID {
		t.Fatalf("fork-after anchor = %d, want the chosen response %d", result.ForkAfterMessageID, answer.ID)
	}
	tree, err := store.GetBranchTree(ctx, result.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range tree.Nodes {
		if node.SessionID == result.Session.ID && node.ForkAfterSequence != lastTool.Sequence {
			t.Fatalf("fork-after sequence = %d, want last tool row %d", node.ForkAfterSequence, lastTool.Sequence)
		}
	}

	all, err := store.GetMessages(ctx, source.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	later, err := MessagesAfterBranchAnchor(all, answer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 2 || later[0].Role != core.RoleUser || later[1].Role != core.RoleAssistant {
		t.Fatalf("later messages = %#v, want only the next user turn", later)
	}
}
