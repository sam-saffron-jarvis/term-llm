package core

import "strings"

func CollectTextParts(parts []Part) string {
	var b strings.Builder
	for _, part := range parts {
		if (part.Type == PartText || part.Type == PartFile) && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func CollectToolResultText(parts []Part) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Type == PartToolResult && part.ToolResult != nil {
			b.WriteString(ToolResultTextContent(part.ToolResult))
		}
	}
	return b.String()
}

func FlattenSystemUser(messages []Message) (string, string) {
	var systemParts []string
	var userParts []string
	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemParts = append(systemParts, CollectTextParts(msg.Parts))
		case RoleUser:
			userParts = append(userParts, CollectTextParts(msg.Parts))
		case RoleTool:
			userParts = append(userParts, CollectToolResultText(msg.Parts))
		}
	}
	return strings.Join(systemParts, "\n\n"), strings.Join(userParts, "\n\n")
}

// IsDirectSkillActivation reports whether role and parts form the hidden
// developer row that holds a user's main-context skill instructions. Clients
// write it directly before the user row that invokes the skill, so it belongs
// to that turn. Isolated-run results carry a run ID and do not.
func IsDirectSkillActivation(role Role, parts []Part) bool {
	if role != RoleDeveloper {
		return false
	}
	for _, part := range parts {
		if part.Type == PartSkillActivation && part.SkillActivation != nil {
			return part.SkillActivation.Execution == "main" && part.SkillActivation.RunID == ""
		}
	}
	return false
}

// InsertRequestContext restores request-only instructions before their admitting
// user turn (one-based), preserving native delivery cursors as history grows.
// Zero or an absent turn falls back to the leading system/developer prefix.
func InsertRequestContext(messages []Message, contextMessage Message, userTurn int) []Message {
	insertAt := 0
	for insertAt < len(messages) && (messages[insertAt].Role == RoleSystem || messages[insertAt].Role == RoleDeveloper) {
		insertAt++
	}
	seen := 0
	for i, message := range messages {
		if message.Role == RoleUser {
			seen++
			if seen == userTurn {
				insertAt = i
				break
			}
		}
	}
	out := make([]Message, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, contextMessage)
	return append(out, messages[insertAt:]...)
}
