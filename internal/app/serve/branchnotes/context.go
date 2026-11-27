package branchnotes

import (
	"strings"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/session"
)

type BranchTreePoint struct {
	MessageID         int64  `json:"message_id"`
	AnchorMessageID   int64  `json:"anchor_message_id,omitempty"`
	Sequence          int    `json:"sequence"`
	Role              string `json:"role"`
	Preview           string `json:"preview"`
	Prefill           string `json:"prefill,omitempty"`
	LaterMessageCount int    `json:"later_message_count"`
}

func branchContextSourceMessage(message session.Message) bool {
	if session.IsBranchableMessage(message) {
		return true
	}
	if message.Role == core.RoleDeveloper {
		_, ok := message.PathNoteProvenance()
		return ok
	}
	return false
}

func BranchTreePoints(messages []session.Message, activeAnchorRowID int64) []BranchTreePoint {
	if activeAnchorRowID < 0 {
		return nil
	}
	if activeAnchorRowID > 0 {
		activeAnchorIndex := -1
		for i := range messages {
			if messages[i].ID == activeAnchorRowID {
				activeAnchorIndex = i
				break
			}
		}
		if activeAnchorIndex < 0 {
			return nil
		}
		messages = messages[:activeAnchorIndex+1]
	}
	after := make(map[int64]int, len(messages))
	contextCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		after[messages[i].ID] = contextCount
		if branchContextSourceMessage(messages[i]) {
			contextCount++
		}
	}
	points := make([]BranchTreePoint, 0, len(messages))
	previousContinuationID := int64(0)
	for _, message := range messages {
		if message.CompactionTail || engine.IsInternalCompactionSummaryText(message.TextContent) {
			continue
		}
		if message.Role == core.RoleAssistant {
			previousContinuationID = message.ID
			continue
		}
		if message.Role != core.RoleUser {
			continue
		}
		anchorID := previousContinuationID
		laterCount := contextCount
		if anchorID > 0 {
			laterCount = after[anchorID]
		}
		if laterCount > 0 {
			laterCount--
		}
		preview := session.TruncateSummary(strings.Join(strings.Fields(message.TextContent), " "))
		if preview == "" {
			preview = "(attachment content)"
		}
		points = append(points, BranchTreePoint{
			MessageID: message.ID, AnchorMessageID: anchorID, Sequence: message.Sequence,
			Role: string(core.RoleUser), Preview: preview, Prefill: message.TextContent, LaterMessageCount: laterCount,
		})
	}
	return points
}

func PruneActiveBranchOutput(messages []session.Message, activeResponseID string, activeAnchorRowID int64) []session.Message {
	if strings.TrimSpace(activeResponseID) == "" {
		return messages
	}
	if activeAnchorRowID <= 0 {
		return nil
	}
	for i := range messages {
		if messages[i].ID == activeAnchorRowID {
			return append([]session.Message(nil), messages[:i+1]...)
		}
	}
	return nil
}

func DirectBranchNode(tree session.BranchTree, childSessionID string) (session.BranchTreeNode, bool) {
	for _, node := range tree.Nodes {
		if node.SessionID == childSessionID && node.ParentSessionID != "" {
			return node, true
		}
	}
	return session.BranchTreeNode{}, false
}

func ExistingPathNote(messages []session.Message, sourceSessionID string, anchorMessageID int64) bool {
	for i := range messages {
		provenance, ok := messages[i].PathNoteProvenance()
		if ok && provenance.SourceSessionID == sourceSessionID && provenance.AnchorMessageID == anchorMessageID {
			return true
		}
	}
	return false
}
