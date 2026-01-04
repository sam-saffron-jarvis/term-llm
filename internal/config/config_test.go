package config

import "testing"

func TestApplyOverrides(t *testing.T) {
	cfg := &Config{
		Provider: "anthropic",
		Anthropic: AnthropicConfig{
			Model: "claude-sonnet-4-5",
		},
		OpenAI: OpenAIConfig{
			Model: "gpt-5.2",
		},
		Gemini: GeminiConfig{
			Model: "gemini-3-flash-preview",
		},
	}

	cfg.ApplyOverrides("openai", "gpt-4o")
	if cfg.Provider != "openai" {
		t.Fatalf("provider=%q, want %q", cfg.Provider, "openai")
	}
	if cfg.OpenAI.Model != "gpt-4o" {
		t.Fatalf("openai model=%q, want %q", cfg.OpenAI.Model, "gpt-4o")
	}
	if cfg.Anthropic.Model != "claude-sonnet-4-5" {
		t.Fatalf("anthropic model changed unexpectedly: %q", cfg.Anthropic.Model)
	}

	cfg.ApplyOverrides("", "gemini-2.5-flash")
	if cfg.Provider != "openai" {
		t.Fatalf("provider changed unexpectedly: %q", cfg.Provider)
	}
	if cfg.OpenAI.Model != "gemini-2.5-flash" {
		t.Fatalf("openai model=%q, want %q", cfg.OpenAI.Model, "gemini-2.5-flash")
	}
}
