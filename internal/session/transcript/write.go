package transcript

import (
	"context"
	"errors"
	"fmt"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/session/notificationdb"
)

// ResponseRunOwnedOutputKeys identifies response-owned assistant segments, tool
// results and events for durable output accounting, independently of content.
func ResponseRunOwnedOutputKeys(runID string, messages []core.Message) []string {
	keys := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.ResponseID != runID {
			continue
		}
		switch msg.Role {
		case core.RoleAssistant:
			keys = append(keys, fmt.Sprintf("assistant:%d", msg.AssistantSegmentOrdinal))
		case core.RoleTool:
			callID := ""
			for _, part := range msg.Parts {
				if part.ToolResult != nil && part.ToolResult.ID != "" {
					callID = part.ToolResult.ID
					break
				}
				if part.ToolCall != nil && part.ToolCall.ID != "" {
					callID = part.ToolCall.ID
					break
				}
			}
			if callID != "" {
				keys = append(keys, "tool:"+callID)
			} else {
				keys = append(keys, "")
			}
		case core.RoleEvent:
			if msg.SegmentStartSequence > 0 || msg.SegmentEndSequence > 0 {
				keys = append(keys, fmt.Sprintf("event:%d:%d", msg.SegmentStartSequence, msg.SegmentEndSequence))
			} else {
				keys = append(keys, "")
			}
		}
	}
	return keys
}

// AddMessageWithTranscriptRev writes a row and reports its committed revision
// when supported, falling back only for stores without revision capability.
func AddMessageWithTranscriptRev(ctx context.Context, store session.Store, sessionID string, message *session.Message) (int64, error) {
	writer, ok := store.(session.TranscriptRevisionWriter)
	if !ok {
		if err := store.AddMessage(ctx, sessionID, message); err != nil {
			return 0, err
		}
		return 0, nil
	}
	rev, err := writer.AddMessageWithTranscriptRev(ctx, sessionID, message)
	if errors.Is(err, session.ErrTranscriptRevisionUnsupported) {
		if err := store.AddMessage(ctx, sessionID, message); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return rev, err
}

// WriteTranscriptWithRev writes through ReplaceMessages, or ResetMessages
// when reset, reporting the committed transcript revision when supported.
func WriteTranscriptWithRev(ctx context.Context, store session.Store, sessionID string, messages []session.Message, reset bool) (int64, error) {
	plain := store.ReplaceMessages
	if reset {
		plain = store.ResetMessages
	}
	if writer, ok := store.(session.TranscriptRevisionWriter); ok {
		withRev := writer.ReplaceMessagesWithTranscriptRev
		if reset {
			withRev = writer.ResetMessagesWithTranscriptRev
		}
		if rev, err := withRev(ctx, sessionID, messages); !errors.Is(err, session.ErrTranscriptRevisionUnsupported) {
			return rev, err
		}
	}
	return 0, plain(ctx, sessionID, messages)
}

// UpdateStreamingMessageWithTranscriptRev upserts streaming content and reports
// the committed revision when supported by the store.
func UpdateStreamingMessageWithTranscriptRev(ctx context.Context, store session.Store, sessionID string, message *session.Message, finalizeText bool) (int64, error) {
	writer, ok := store.(session.TranscriptRevisionWriter)
	if !ok {
		if err := session.UpdateStreamingMessage(ctx, store, sessionID, message, finalizeText); err != nil {
			return 0, err
		}
		return 0, nil
	}
	rev, err := writer.UpdateStreamingMessageWithTranscriptRev(ctx, sessionID, message, finalizeText)
	if errors.Is(err, session.ErrTranscriptRevisionUnsupported) {
		if err := session.UpdateStreamingMessage(ctx, store, sessionID, message, finalizeText); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return rev, err
}

// LinkedToolHandoffReader is an optional read capability for durable peer handoffs.
type LinkedToolHandoffReader interface {
	PendingLinkedToolHandoff(context.Context, int64, string) ([]string, error)
}

func AsLinkedToolHandoffReader(store session.Store) (LinkedToolHandoffReader, bool) {
	if logging, ok := store.(*session.LoggingStore); ok {
		return AsLinkedToolHandoffReader(logging.Store)
	}
	reader, ok := store.(LinkedToolHandoffReader)
	return reader, ok
}

func ReadLinkedToolReceipt(ctx context.Context, store session.Store, sessionID, deviceID, key string) (notificationdb.LinkedToolReceipt, error) {
	if logging, ok := store.(*session.LoggingStore); ok {
		return ReadLinkedToolReceipt(ctx, logging.Store, sessionID, deviceID, key)
	}
	if reader, ok := store.(interface {
		ReadLinkedToolReceipt(context.Context, string, string, string) (notificationdb.LinkedToolReceipt, error)
	}); ok {
		return reader.ReadLinkedToolReceipt(ctx, sessionID, deviceID, key)
	}
	return notificationdb.LinkedToolReceipt{}, nil
}
