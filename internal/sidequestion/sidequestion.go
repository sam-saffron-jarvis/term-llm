package sidequestion

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
)

const (
	HistoryLimit = 20
	SystemPolicy = `This is a private side question about the current conversation.
Answer it directly in one response.
The main conversation continues independently.
You have no tools and cannot inspect files, run commands, search, delegate, or take actions.
Use only the supplied conversation and side-question history.
If the answer is not available there, say so.
Do not promise future action.`
	ToolAttemptResponse = "Side questions cannot use tools. Ask using only the supplied conversation context."
)

type Entry struct {
	Question  string    `json:"question"`
	Response  string    `json:"response"`
	CreatedAt time.Time `json:"created_at"`
	Usage     llm.Usage `json:"usage"`
}

type Result struct {
	Response  string
	Usage     llm.Usage
	Synthetic bool
}

// PrepareContextSnapshot returns a provider-safe point-in-time deep copy. It
// removes cache anchors and incomplete tool protocol fragments.
func PrepareContextSnapshot(messages []llm.Message) []llm.Message {
	copied := deepCopyMessages(messages)
	calls := make(map[string]struct{})
	results := make(map[string]struct{})
	for _, msg := range copied {
		for _, part := range msg.Parts {
			if part.ToolCall != nil && strings.TrimSpace(part.ToolCall.ID) != "" {
				calls[part.ToolCall.ID] = struct{}{}
			}
			if part.ToolResult != nil && strings.TrimSpace(part.ToolResult.ID) != "" {
				results[part.ToolResult.ID] = struct{}{}
			}
		}
	}
	complete := make(map[string]struct{})
	for id := range calls {
		if _, ok := results[id]; ok {
			complete[id] = struct{}{}
		}
	}
	out := make([]llm.Message, 0, len(copied))
	for _, msg := range copied {
		if msg.Role == llm.RoleEvent {
			continue
		}
		msg.CacheAnchor = false
		partial := false
		parts := make([]llm.Part, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			if part.ToolCall != nil {
				if _, ok := complete[part.ToolCall.ID]; !ok {
					partial = true
					continue
				}
			}
			if part.ToolResult != nil {
				if _, ok := complete[part.ToolResult.ID]; !ok {
					partial = true
					continue
				}
			}
			parts = append(parts, part)
		}
		if partial {
			filtered := parts[:0]
			for _, part := range parts {
				if part.Type != llm.PartProviderReplay {
					filtered = append(filtered, part)
				}
			}
			parts = filtered
		}
		msg.Parts = parts
		if len(parts) > 0 {
			out = append(out, msg)
		}
	}
	return out
}

func deepCopyMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		out[i].Parts = append([]llm.Part(nil), msg.Parts...)
		for j := range out[i].Parts {
			part := &out[i].Parts[j]
			if part.ToolCall != nil {
				call := *part.ToolCall
				call.Arguments = append([]byte(nil), call.Arguments...)
				part.ToolCall = &call
			}
			if part.ToolResult != nil {
				result := *part.ToolResult
				part.ToolResult = &result
			}
		}
	}
	return out
}

func BuildMessages(snapshot []llm.Message, history []Entry, question string) []llm.Message {
	messages := append([]llm.Message(nil), PrepareContextSnapshot(snapshot)...)
	messages = append(messages, llm.Message{Role: llm.RoleDeveloper, Parts: []llm.Part{{Type: llm.PartText, Text: SystemPolicy}}})
	for _, entry := range history {
		messages = append(messages, llm.UserText(entry.Question), llm.AssistantText(entry.Response))
	}
	return append(messages, llm.UserText(strings.TrimSpace(question)))
}

func AppendHistory(history []Entry, entry Entry) []Entry {
	if strings.TrimSpace(entry.Response) == "" {
		return history
	}
	history = append(history, entry)
	if len(history) > HistoryLimit {
		history = append([]Entry(nil), history[len(history)-HistoryLimit:]...)
	}
	return history
}

// Run performs exactly one provider request. It bypasses the agent engine so no
// local, MCP, approval, delegation, or provider-native search capability exists.
func Run(ctx context.Context, provider llm.Provider, req llm.Request, emit func(llm.Event)) (Result, error) {
	req.Ephemeral = true
	req.SessionID = ""
	req.Tools = nil
	req.ToolMap = nil
	req.ToolChoice = llm.ToolChoice{}
	req.LastTurnToolChoice = nil
	req.ParallelToolCalls = false
	req.Search = false
	req.ForceExternalSearch = false
	req.DisableExternalWebFetch = true
	req.MaxTurns = 1
	req.Responses = &llm.ResponsesOptions{
		ReasoningMode:           reqReasoningMode(req),
		MultiAgent:              llm.MultiAgentOptions{Enabled: false, EnabledSet: true},
		ProgrammaticToolCalling: llm.ProgrammaticToolCallingOptions{Enabled: false, EnabledSet: true},
	}
	stream, err := provider.Stream(ctx, req)
	if err != nil {
		return Result{}, err
	}
	defer stream.Close()
	var response strings.Builder
	var usage llm.Usage
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{Response: response.String(), Usage: usage}, err
		}
		switch event.Type {
		case llm.EventTextDelta:
			response.WriteString(event.Text)
		case llm.EventAttemptDiscard:
			response.Reset()
		case llm.EventUsage:
			if event.Use != nil {
				usage.Add(*event.Use)
			}
		case llm.EventToolCall, llm.EventToolExecStart, llm.EventToolExecEnd:
			warning := llm.Event{Type: llm.EventTextDelta, Text: ToolAttemptResponse}
			if emit != nil {
				emit(warning)
			}
			return Result{Response: ToolAttemptResponse, Usage: usage, Synthetic: true}, nil
		case llm.EventError:
			if event.Err != nil {
				return Result{Response: response.String(), Usage: usage}, event.Err
			}
		}
		if emit != nil {
			emit(event)
		}
	}
	return Result{Response: strings.TrimSpace(response.String()), Usage: usage}, nil
}

func reqReasoningMode(req llm.Request) string {
	if req.Responses != nil {
		return req.Responses.ReasoningMode
	}
	return ""
}
