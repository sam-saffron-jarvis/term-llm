package requestmeta

import (
	"net/url"
	"strings"
)

const maxSetting = 1<<31 - 1

func (s *settings) extract(m *Metadata, path, profile, upstream string) {
	m.Model = s.model("model")
	m.Stream = s.boolean("stream")
	m.DeclaredTools = s.integer("declared_tools", 0, 256)
	switch m.Protocol {
	case "responses":
		s.responses(m, upstream)
	case "chat_completions":
		s.chat(m, profile)
	case "messages", "bedrock":
		s.messages(m)
	case "gemini":
		s.gemini(m, path)
	case "ollama":
		s.ollama(m)
	}
	if m.Protocol == "bedrock" {
		model, ok := bedrockModelFromPath(path)
		if ok {
			m.Model = model
		} else {
			m.Model = ""
			s.bad = true
		}
	}
}

func (s *settings) sampling(m *Metadata, temp, top string, max float64) {
	m.Temperature = s.number(temp, max)
	m.TopP = s.number(top, 1)
}
func (s *settings) openTools(m *Metadata) {
	m.ToolChoice = s.enum("tool_choice", "none", "auto", "required")
	if _, ok := s.values["tool_choice.type"]; ok {
		if m.Protocol == "responses" {
			m.ToolChoice = s.enum("tool_choice.type", "function", "allowed_tools")
		} else {
			m.ToolChoice = s.enum("tool_choice.type", "function")
		}
	}
	m.ParallelTools = s.boolean("parallel_tool_calls")
}
func (s *settings) responses(m *Metadata, upstream string) {
	m.ReasoningEffort = s.enum("reasoning.effort", "none", "minimal", "low", "medium", "high", "xhigh", "max")
	m.ReasoningSummary = s.enum("reasoning.summary", "auto", "concise", "detailed")
	m.OutputLimit = s.integer("max_output_tokens", 0, maxSetting)
	m.ServiceTier = s.enum("service_tier", "auto", "default", "flex", "priority", "scale")
	m.Verbosity = s.enum("text.verbosity", "low", "medium", "high")
	s.sampling(m, "temperature", "top_p", 2)
	s.openTools(m)
	// These extensions have model- and origin-specific evidence, not generic
	// Responses support. Do not interpret lookalikes on arbitrary compatible APIs.
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "https" || u.Host != "api.openai.com" || strings.TrimSuffix(u.Path, "/") != "/v1" || !(m.Model == "gpt-5.6" || strings.HasPrefix(m.Model, "gpt-5.6-")) {
		return
	}
	m.ReasoningMode = s.enum("reasoning.mode", "standard", "pro")
	m.ReasoningContext = s.enum("reasoning.context", "auto", "current_turn", "all_turns")
	m.MultiAgent = s.boolean("multi_agent.enabled")
	m.AgentLimit = s.integer("multi_agent.max_concurrent_subagents", 1, 128)
	m.CacheMode = s.enum("prompt_cache_options.mode", "implicit", "explicit")
	m.CacheTTL = s.enum("prompt_cache_options.ttl", "30m")
}
func (s *settings) chat(m *Metadata, profile string) {
	m.ReasoningEffort = s.enum("reasoning_effort", "none", "minimal", "low", "medium", "high", "xhigh", "max")
	if _, ok := s.values["max_completion_tokens"]; ok {
		m.OutputLimit = s.integer("max_completion_tokens", 0, maxSetting)
	} else {
		m.OutputLimit = s.integer("max_tokens", 0, maxSetting)
	}
	m.ServiceTier = s.enum("service_tier", "auto", "default", "flex", "priority", "scale")
	s.sampling(m, "temperature", "top_p", 2)
	s.openTools(m)
	if profile != "vllm" {
		return
	}
	// The vLLM adapter emits exactly these boolean template flags. Arbitrary
	// configured template parameter names are intentionally not probed.
	a := s.boolean("chat_template_kwargs.thinking")
	b := s.boolean("chat_template_kwargs.enable_thinking")
	if a != nil && b != nil && *a != *b {
		s.bad = true
	} else if a != nil {
		m.ThinkingMode = onOff(*a)
	} else if b != nil {
		m.ThinkingMode = onOff(*b)
	}
	m.ThinkingBudget = s.integer("thinking_token_budget", 0, maxSetting)
}
func (s *settings) messages(m *Metadata) {
	m.ReasoningEffort = s.enum("output_config.effort", "low", "medium", "high", "max")
	m.ThinkingMode = s.enum("thinking.type", "enabled", "disabled", "adaptive")
	m.ThinkingBudget = s.integer("thinking.budget_tokens", 0, maxSetting)
	m.OutputLimit = s.integer("max_tokens", 0, maxSetting)
	s.sampling(m, "temperature", "top_p", 1)
	m.ToolChoice = s.enum("tool_choice.type", "auto", "any", "none", "tool")
	if disabled := s.boolean("tool_choice.disable_parallel_tool_use"); disabled != nil {
		v := !*disabled
		m.ParallelTools = &v
	}
	m.ServiceTier = s.enum("service_tier", "auto", "standard_only")
}
func (s *settings) gemini(m *Metadata, path string) {
	if at := strings.LastIndex(path, "/models/"); at >= 0 {
		model, ok := geminiModel(path[at:])
		if ok {
			m.Model = model
		} else {
			m.Model = ""
			s.bad = true
		}
	}
	m.ReasoningEffort = s.enum("generationConfig.thinkingConfig.thinkingLevel", "MINIMAL", "LOW", "MEDIUM", "HIGH")
	if n := s.integer("generationConfig.thinkingConfig.thinkingBudget", -1, maxSetting); n != nil {
		if *n == -1 {
			m.ThinkingBudgetMode = "dynamic"
		} else {
			m.ThinkingBudget = n
		}
	}
	m.OutputLimit = s.integer("generationConfig.maxOutputTokens", 0, maxSetting)
	s.sampling(m, "generationConfig.temperature", "generationConfig.topP", 2)
	m.ToolChoice = s.enum("toolConfig.functionCallingConfig.mode", "AUTO", "NONE", "ANY")
}
func (s *settings) ollama(m *Metadata) {
	if raw, ok := s.values["think"]; ok {
		if string(raw) == "true" || string(raw) == "false" {
			m.ThinkingMode = onOff(string(raw) == "true")
		} else {
			m.ReasoningEffort = s.enum("think", "low", "medium", "high", "max")
		}
	}
	if n := s.integer("options.num_predict", -2, maxSetting); n != nil {
		switch *n {
		case -1:
			m.OutputLimitMode = "unlimited"
		case -2:
			m.OutputLimitMode = "context"
		default:
			m.OutputLimit = n
		}
	}
	s.sampling(m, "options.temperature", "options.top_p", 2)
}
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
