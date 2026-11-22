package wire

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// MaxResponseClientMessageIDLength bounds first-party request/message identifiers.
const MaxResponseClientMessageIDLength = 200

// ResponseIdempotencyKey reads the standard key, then supported UI aliases.
func ResponseIdempotencyKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	// Prefer the standard header. X-Term-LLM-Request-ID is accepted for the
	// first-party UI and is only registered for streaming
	// stateful response runs (see runIdempotencyKey above).
	for _, name := range []string{"Idempotency-Key", "X-Idempotency-Key", "X-Term-LLM-Request-ID"} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// ValidResponseDraftID recognizes client-generated draft conversation IDs.
func ValidResponseDraftID(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "draft_") || len(value) <= len("draft_") || len(value) > 200 {
		return false
	}
	for _, r := range value[len("draft_"):] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// AppendResponsePassthroughTools merges tools without duplicating mapped server tools.
func AppendResponsePassthroughTools(serverTools []core.ToolSpec, passthroughTools []core.ToolSpec, toolMap map[string]string) []core.ToolSpec {
	if len(passthroughTools) == 0 {
		return serverTools
	}

	selected := make(map[string]bool, len(serverTools))
	for _, spec := range serverTools {
		selected[spec.Name] = true
	}

	for _, spec := range passthroughTools {
		if selected[spec.Name] {
			continue
		}
		if mapped, ok := toolMap[spec.Name]; ok && selected[mapped] {
			continue
		}
		serverTools = append(serverTools, spec)
		selected[spec.Name] = true
	}

	return serverTools
}

// PrepareResponseClientMessageIDs validates the first-party user batch identity contract.
func PrepareResponseClientMessageIDs(messages []core.Message, requestID string, firstParty bool) (bool, error) {
	requestID = strings.TrimSpace(requestID)
	if !firstParty {
		// Item-level identities are reserved for the first-party UI contract.
		for i := range messages {
			messages[i].ClientMessageID = ""
		}
		if requestID != "" {
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == core.RoleUser {
					messages[i].ClientMessageID = requestID
					break
				}
			}
		}
		return false, nil
	}

	userIndexes := make([]int, 0, len(messages))
	for i := range messages {
		if messages[i].Role == core.RoleUser {
			userIndexes = append(userIndexes, i)
		}
	}
	identifiedBatch := len(messages) > 1 && len(userIndexes) == len(messages)
	if !identifiedBatch {
		if len(userIndexes) > 0 {
			last := userIndexes[len(userIndexes)-1]
			itemID := strings.TrimSpace(messages[last].ClientMessageID)
			if itemID != "" && itemID != requestID {
				return false, fmt.Errorf("top-level client_message_id must match the final user message")
			}
			for _, index := range userIndexes {
				messages[index].ClientMessageID = ""
			}
			messages[last].ClientMessageID = requestID
		}
		return false, nil
	}

	ids := make([]string, 0, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for i := range messages {
		id := strings.TrimSpace(messages[i].ClientMessageID)
		if id == "" {
			return false, fmt.Errorf("each batched user message requires client_message_id")
		}
		if len(id) > MaxResponseClientMessageIDLength {
			return false, fmt.Errorf("client_message_id is too long")
		}
		if _, duplicate := seen[id]; duplicate {
			return false, fmt.Errorf("duplicate client_message_id %q in user batch", id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if ids[len(ids)-1] != requestID {
		return false, fmt.Errorf("top-level client_message_id must match the final batched user message")
	}
	return true, nil
}

// ResponseClientMessageIDs returns the user-message IDs used by response admission.
func ResponseClientMessageIDs(messages []core.Message) []string {
	ids := make([]string, 0, len(messages))
	for i := range messages {
		if messages[i].Role != core.RoleUser {
			continue
		}
		if id := strings.TrimSpace(messages[i].ClientMessageID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
