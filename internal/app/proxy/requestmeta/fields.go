package requestmeta

import "strings"

// Scope structural validation to the actual wire family. Unknown extensions
// still get full JSON validation, but cannot invalidate unrelated settings.
const messageFields = `tools max_tokens temperature top_p service_tier
 thinking thinking.type thinking.budget_tokens output_config output_config.effort
 tool_choice tool_choice.type tool_choice.disable_parallel_tool_use`

var protocolFields = map[string]map[string]bool{
	"responses": fieldSet(`model stream tools max_output_tokens service_tier temperature top_p parallel_tool_calls
 reasoning reasoning.effort reasoning.mode reasoning.context reasoning.summary text text.verbosity
 multi_agent multi_agent.enabled multi_agent.max_concurrent_subagents prompt_cache_options prompt_cache_options.mode prompt_cache_options.ttl
 tool_choice tool_choice.type`),
	"chat_completions": fieldSet(`model stream tools reasoning_effort max_tokens max_completion_tokens service_tier temperature top_p
 parallel_tool_calls tool_choice tool_choice.type chat_template_kwargs chat_template_kwargs.thinking chat_template_kwargs.enable_thinking thinking_token_budget`),
	"messages": fieldSet(messageFields + ` model stream`),
	"bedrock":  fieldSet(messageFields + ` anthropic_version`),
	"gemini": fieldSet(`model tools generationConfig generationConfig.thinkingConfig generationConfig.thinkingConfig.thinkingLevel
 generationConfig.thinkingConfig.thinkingBudget generationConfig.maxOutputTokens generationConfig.temperature generationConfig.topP
 toolConfig toolConfig.functionCallingConfig toolConfig.functionCallingConfig.mode`),
	"ollama": fieldSet(`model stream tools think options options.num_predict options.temperature options.top_p`),
}

func fieldSet(fields string) map[string]bool {
	set := make(map[string]bool)
	for _, field := range strings.Fields(fields) {
		set[field] = true
	}
	return set
}
func (s *settings) fieldAllowed(name string) bool {
	if !protocolFields[s.protocol][name] {
		return false
	}
	if s.protocol == "gemini" && name == "model" && !s.wrapped {
		return false
	}
	if s.protocol == "chat_completions" && s.profile != "vllm" && (strings.HasPrefix(name, "chat_template_kwargs") || name == "thinking_token_budget") {
		return false
	}
	return true
}
