package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func ResponseRequestFingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode response request fingerprint: %w", err)
	}
	// Decode and encode once so nested RawMessage objects are normalized too:
	// insignificant whitespace and object member ordering must not turn a replay
	// into a conflict.
	var semantic any
	if err := json.Unmarshal(encoded, &semantic); err != nil {
		return "", fmt.Errorf("normalize response request fingerprint: %w", err)
	}
	canonical, err := json.Marshal(semantic)
	if err != nil {
		return "", fmt.Errorf("canonicalize response request fingerprint: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func ParseResponsesInput(input json.RawMessage, parseUserContent func(json.RawMessage) (core.Message, error)) ([]core.Message, bool, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed == "null" {
		return nil, false, fmt.Errorf("input is required")
	}

	// string shorthand
	var inputText string
	if err := json.Unmarshal(input, &inputText); err == nil {
		if strings.TrimSpace(inputText) == "" {
			return nil, false, fmt.Errorf("input is empty")
		}
		return []core.Message{core.UserText(inputText)}, false, nil
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, false, fmt.Errorf("invalid input format")
	}

	var messages []core.Message
	callNameByID := map[string]string{}
	allToolItems := len(items) > 0
	for _, item := range items {
		itemType := JSONStringValue(item["type"])
		if itemType != "function_call" && itemType != "function_call_output" {
			allToolItems = false
			break
		}
	}
	replaceHistory := len(items) > 1 && !allToolItems
	userCount := 0

	for _, item := range items {
		itemType := JSONStringValue(item["type"])
		switch itemType {
		case "message":
			role := strings.ToLower(strings.TrimSpace(JSONStringValue(item["role"])))
			switch role {
			case "developer":
				messages = append(messages, core.Message{Role: core.RoleDeveloper, Parts: []core.Part{{Type: core.PartText, Text: ExtractMessageText(item["content"])}}})
				replaceHistory = true
			case "system":
				messages = append(messages, core.SystemText(ExtractMessageText(item["content"])))
				replaceHistory = true
			case "assistant":
				messages = append(messages, core.AssistantText(ExtractMessageText(item["content"])))
				replaceHistory = true
			default:
				msg, err := parseUserContent(item["content"])
				if err != nil {
					return nil, false, fmt.Errorf("user message: %w", err)
				}
				msg.ClientMessageID = strings.TrimSpace(JSONStringValue(item["client_message_id"]))
				messages = append(messages, msg)
				userCount++
			}
		case "function_call":
			id := JSONStringValue(item["call_id"])
			name := JSONStringValue(item["name"])
			args := JSONStringValue(item["arguments"])
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			callNameByID[id] = name
			messages = append(messages, core.Message{Role: core.RoleAssistant, Parts: []core.Part{{
				Type:     core.PartToolCall,
				ToolCall: &core.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)},
			}}})
		case "function_call_output":
			id := JSONStringValue(item["call_id"])
			out := JSONStringValue(item["output"])
			messages = append(messages, core.ToolResultMessage(id, callNameByID[id], out, nil))
		}
	}

	if userCount > 1 {
		replaceHistory = true
	}
	return messages, replaceHistory, nil
}

func ExtractMessageText(content json.RawMessage) string {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			pType := strings.ToLower(strings.TrimSpace(JSONStringValue(p["type"])))
			switch pType {
			case "text", "input_text", "output_text":
				b.WriteString(JSONStringValue(p["text"]))
			}
		}
		return b.String()
	}
	return ""
}

func JSONStringValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func ParseRequestedTools(raw []json.RawMessage) (bool, bool, map[string]bool, []core.ToolSpec) {
	search := false
	ptc := false
	toolNames := map[string]bool{}
	passthrough := make([]core.ToolSpec, 0, len(raw))

	for _, item := range raw {
		var generic map[string]json.RawMessage
		if err := json.Unmarshal(item, &generic); err != nil {
			continue
		}
		typeName := strings.ToLower(strings.TrimSpace(JSONStringValue(generic["type"])))
		switch typeName {
		case "web_search_preview", "web_search":
			search = true
		case "programmatic_tool_calling":
			ptc = true
		case "function":
			spec, ok := core.ParseClientFunctionTool(generic)
			if !ok {
				continue
			}
			toolNames[spec.Name] = true
			passthrough = append(passthrough, spec)
		}
	}

	return search, ptc, toolNames, passthrough
}

func ResponsesUsagePayload(usage core.Usage) map[string]any {
	inputTokens := usage.ProviderRawInputTokens
	if inputTokens <= 0 {
		inputTokens = usage.InputTokens + usage.CachedInputTokens + usage.CacheWriteTokens
	}
	totalTokens := usage.ProviderTotalTokens
	if totalTokens <= 0 {
		totalTokens = inputTokens + usage.OutputTokens
	}
	return map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  totalTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens":      usage.CachedInputTokens,
			"cache_write_tokens": usage.CacheWriteTokens,
		},
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}
