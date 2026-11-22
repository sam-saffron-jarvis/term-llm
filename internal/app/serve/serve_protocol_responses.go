package serve

import (
	"encoding/json"
	"strings"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/llm"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/tools"
)

type responsesModelSwapRequest struct {
	Mode     string `json:"mode,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

type responsesReasoningRequest struct {
	Effort  string `json:"effort,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Context string `json:"context,omitempty"`
}

type responsesMultiAgentRequest struct {
	Enabled                bool `json:"enabled"`
	MaxConcurrentSubagents int  `json:"max_concurrent_subagents,omitempty"`
}

type responsesPromptCacheRequest struct {
	Mode string `json:"mode,omitempty"`
	TTL  string `json:"ttl,omitempty"`
}

type responsesBranchContextRequest struct {
	Mode  string `json:"mode,omitempty"`
	Focus string `json:"focus,omitempty"`
}

type responsesCreateRequest struct {
	UIContext           *tools.UIBrowserContext        `json:"ui_context,omitempty"`
	Agent               string                         `json:"agent,omitempty"`
	Model               string                         `json:"model"`
	Provider            string                         `json:"provider"`
	Input               json.RawMessage                `json:"input"`
	Tools               []json.RawMessage              `json:"tools,omitempty"`
	IncludeServerTools  bool                           `json:"include_server_tools,omitempty"`
	ToolChoice          json.RawMessage                `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                          `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens     int                            `json:"max_output_tokens,omitempty"`
	Temperature         *float32                       `json:"temperature,omitempty"`
	TopP                *float32                       `json:"top_p,omitempty"`
	Stream              bool                           `json:"stream,omitempty"`
	PreviousResponseID  string                         `json:"previous_response_id,omitempty"`
	Branch              bool                           `json:"branch,omitempty"`
	BranchContext       *responsesBranchContextRequest `json:"branch_context,omitempty"`
	ExpectedRev         *int64                         `json:"expected_rev,omitempty"`
	IdempotencyKey      string                         `json:"idempotency_key,omitempty"`
	ClientMessageID     string                         `json:"client_message_id,omitempty"`
	ReasoningEffort     string                         `json:"reasoning_effort,omitempty"`
	ServiceTier         *string                        `json:"service_tier,omitempty"`
	Reasoning           *responsesReasoningRequest     `json:"reasoning,omitempty"`
	MultiAgent          *responsesMultiAgentRequest    `json:"multi_agent,omitempty"`
	PromptCacheOptions  *responsesPromptCacheRequest   `json:"prompt_cache_options,omitempty"`
	UseDefaultWorkspace bool                           `json:"use_default_workspace,omitempty"`
	NoProject           bool                           `json:"no_project,omitempty"`
	ProjectID           string                         `json:"project_id,omitempty"`
	WorktreeDir         string                         `json:"worktree_dir,omitempty"`
	ModelSwap           *responsesModelSwapRequest     `json:"model_swap,omitempty"`
}

func responseRequestFingerprint(req responsesCreateRequest) (string, error) {
	// Transport keys and viewport diagnostics are not semantic request data.
	req.IdempotencyKey = ""
	req.UIContext = nil
	return wire.ResponseRequestFingerprint(req)
}

func parseResponsesInput(input json.RawMessage) ([]core.Message, bool, error) {
	return wire.ParseResponsesInput(input, parseUserMessageContent)
}

// normalizeReasoningEffort trims whitespace and folds the literal "default"
// value (sent by older clients and stale localStorage entries) to an empty
// string so that providers receive "" meaning "use the provider default"
// rather than a bogus "default" value that would produce an upstream 400.
func normalizeReasoningEffort(value string) string {
	v := strings.TrimSpace(value)
	if strings.EqualFold(v, "default") {
		return ""
	}
	return v
}

// normalizeProviderModelEffort canonicalizes the web/API split between model
// identity and reasoning effort. Older URLs, localStorage values, and CLI-style
// model names may encode effort as a model suffix (for example
// "gpt-5.5-medium"). The web UI has a separate effort selector, so the server
// must not echo a suffixed model and a separate reasoning_effort at the same
// time. Provider-aware parsing avoids false positives such as
// "gpt-5.1-codex-max", where "max" is part of the model name for GPT-5
// providers rather than a supported effort level.
//
// Precedence: an explicit reasoning_effort wins over a suffix. If no explicit
// effort is present, a supported suffix is promoted into reasoning_effort.
func normalizeProviderModelEffort(provider, model, effort string) (string, string) {
	model = strings.TrimSpace(model)
	effort = normalizeReasoningEffort(effort)
	if model == "" {
		return "", effort
	}

	base, suffixEffort := llm.BaseModelAndEffortForProvider(provider, model)
	if base == "" {
		return model, effort
	}
	if suffixEffort == "" {
		return base, effort
	}
	if effort == "" {
		effort = suffixEffort
	}
	return base, effort
}

var parseRequestedTools = wire.ParseRequestedTools
var parseRequestedFunctionTool = core.ParseClientFunctionTool
var responsesUsagePayload = wire.ResponsesUsagePayload

func responsesFinalResponse(result serveRunResult, model string, respID string, created int64) map[string]any {
	output := []map[string]any{}
	if result.Text.Len() > 0 {
		output = append(output, map[string]any{
			"id":   "msg_" + randomSuffix(),
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": result.Text.String(),
			}},
		})
	}
	for _, call := range result.ToolCalls {
		output = append(output, map[string]any{
			"id":        "fc_" + call.ID,
			"type":      "function_call",
			"call_id":   call.ID,
			"name":      call.Name,
			"arguments": string(call.Arguments),
		})
	}

	response := map[string]any{
		"id":            respID,
		"object":        "response",
		"created":       created,
		"model":         model,
		"output":        output,
		"usage":         responsesUsagePayload(result.Usage),
		"session_usage": responsesUsagePayload(result.SessionUsage),
		"context_usage": result.ContextUsage,
	}
	return response
}
