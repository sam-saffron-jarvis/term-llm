package llm

import (
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestCreateProviderFromConfig_OpenAICompatRequiresProviderName(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("createProviderFromConfig panicked: %v", r)
		}
	}()

	_, err := createProviderFromConfig("", &config.ProviderConfig{
		Type:    config.ProviderTypeOpenAICompat,
		BaseURL: "https://example.com/v1",
		Model:   "test-model",
	})
	if err == nil {
		t.Fatal("expected empty provider name to return an error")
	}
	if !strings.Contains(err.Error(), "non-empty name") {
		t.Fatalf("expected empty name guidance, got %v", err)
	}
}

func TestOpenAICompatReasoningParserOptionsUsesOnlyExplicitConfig(t *testing.T) {
	t.Parallel()

	parseReasoning, includeReasoning, thinkingParam := openAICompatReasoningParserOptions(&config.ProviderConfig{
		Type:    config.ProviderTypeOpenAICompat,
		BaseURL: "https://example.invalid/v1",
	})
	if parseReasoning != nil || includeReasoning != nil || thinkingParam != "" {
		t.Fatalf("reasoning options = %v/%v/%q, want nil/nil/empty", parseReasoning, includeReasoning, thinkingParam)
	}
}

func TestOpenAICompatReasoningParserOptionsReadsExplicitConfig(t *testing.T) {
	t.Parallel()

	no := false
	parseReasoning, includeReasoning, thinkingParam := openAICompatReasoningParserOptions(&config.ProviderConfig{
		Type:             config.ProviderTypeOpenAICompat,
		BaseURL:          "https://example.invalid/v1",
		ParseReasoning:   &no,
		IncludeReasoning: &no,
		ThinkingParam:    "custom_thinking",
	})
	if parseReasoning == nil || *parseReasoning {
		t.Fatalf("parseReasoning = %v, want false", parseReasoning)
	}
	if includeReasoning == nil || *includeReasoning {
		t.Fatalf("includeReasoning = %v, want false", includeReasoning)
	}
	if thinkingParam != "custom_thinking" {
		t.Fatalf("thinkingParam = %q, want custom_thinking", thinkingParam)
	}
}
