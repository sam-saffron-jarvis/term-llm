package llm

import "strings"

// VLLMProvider implements Provider for vLLM's OpenAI-compatible chat API plus
// vLLM/Qwen-specific thinking controls.
type VLLMProvider struct {
	*OpenAICompatProvider
}

func NewVLLMProviderFull(baseURL, chatURL, apiKey, model, name string) *VLLMProvider {
	if strings.TrimSpace(name) == "" {
		name = "vLLM"
	}
	actualModel, effort := ParseModelEffort(model)
	p := NewOpenAICompatProviderFull(baseURL, chatURL, apiKey, actualModel, name, nil)
	p.effort = effort
	p.vllmThinking = true
	return &VLLMProvider{OpenAICompatProvider: p}
}

func NewVLLMProvider(baseURL, apiKey, model, name string) *VLLMProvider {
	return NewVLLMProviderFull(baseURL, "", apiKey, model, name)
}

func vLLMThinkingSettings(effort string) (bool, int) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "default", "minimal", "none", "off", "false":
		return false, 256
	case "low":
		return true, 1024
	case "medium":
		return true, 4096
	case "high", "xhigh", "max":
		return true, 10000
	default:
		return true, 0
	}
}
