package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/samsaffron/term-llm/internal/config"
)

// TranscribeWithConfig transcribes an audio file using the provider configured in cfg.
// providerOverride, if non-empty, overrides cfg.Transcription.Provider.
//
// Supported provider names: "openai" (default), "mistral" (Voxtral), "local" (whisper.cpp server),
// "whisper-cli" (whisper.cpp CLI binary). The whisper-cli case is delegated back to cmd via
// the transcribeWhisperCLI function — callers that don't support it (e.g. Telegram) will get
// an unsupported-provider error, which is intentional.
func TranscribeWithConfig(ctx context.Context, cfg *config.Config, filePath, language, providerOverride string) (string, error) {
	providerName := providerOverride
	if providerName == "" {
		providerName = cfg.Transcription.Provider
	}
	if providerName == "" {
		providerName = "openai"
	}

	modelOverride := cfg.Transcription.Model

	switch providerName {
	case "local":
		// whisper.cpp HTTP server — OpenAI-compatible endpoint, typically no auth required
		endpoint := "http://localhost:8080/inference"
		if providerCfg, ok := cfg.Providers["local_whisper"]; ok && providerCfg.BaseURL != "" {
			endpoint = strings.TrimRight(providerCfg.BaseURL, "/") + "/inference"
		}
		return TranscribeFile(ctx, filePath, TranscribeOptions{
			Endpoint: endpoint,
			Model:    modelOverride,
			Language: language,
		})

	case "mistral":
		mistralCfg := cfg.Providers["mistral"]
		apiKey := mistralCfg.ResolvedAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("MISTRAL_API_KEY")
		}
		if apiKey == "" {
			return "", fmt.Errorf("transcription provider %q has no API key configured (providers.mistral.api_key or MISTRAL_API_KEY)", providerName)
		}
		endpoint := "https://api.mistral.ai/v1/audio/transcriptions"
		if mistralCfg.BaseURL != "" {
			endpoint = strings.TrimRight(mistralCfg.BaseURL, "/") + "/audio/transcriptions"
		}
		model := modelOverride
		if model == "" {
			model = "voxtral-mini-latest"
		}
		return TranscribeFile(ctx, filePath, TranscribeOptions{
			APIKey:   apiKey,
			Endpoint: endpoint,
			Model:    model,
			Language: language,
		})

	case "openai":
		// Named provider entry takes precedence over env var
		if p, ok := cfg.Providers[string(config.ProviderTypeOpenAI)]; ok && p.ResolvedAPIKey != "" {
			endpoint := ""
			if p.BaseURL != "" {
				endpoint = strings.TrimRight(p.BaseURL, "/") + "/audio/transcriptions"
			}
			return TranscribeFile(ctx, filePath, TranscribeOptions{
				APIKey:   p.ResolvedAPIKey,
				Endpoint: endpoint,
				Model:    modelOverride,
				Language: language,
			})
		}
		// Fall back to environment variable
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return "", fmt.Errorf("transcription is not configured — set providers.openai.api_key or configure a transcription provider in config")
		}
		return TranscribeFile(ctx, filePath, TranscribeOptions{
			APIKey:   apiKey,
			Model:    modelOverride,
			Language: language,
		})

	default:
		// Try looking up by name in providers map (allows any openai_compatible provider)
		if p, ok := cfg.Providers[providerName]; ok {
			apiKey := p.ResolvedAPIKey
			if apiKey == "" {
				return "", fmt.Errorf("transcription provider %q has no API key configured", providerName)
			}
			endpoint := ""
			resolvedURL := p.ResolvedURL
			if resolvedURL != "" {
				endpoint = strings.TrimRight(resolvedURL, "/") + "/audio/transcriptions"
			} else if p.BaseURL != "" {
				endpoint = strings.TrimRight(p.BaseURL, "/") + "/audio/transcriptions"
			}
			return TranscribeFile(ctx, filePath, TranscribeOptions{
				APIKey:   apiKey,
				Endpoint: endpoint,
				Model:    modelOverride,
				Language: language,
			})
		}
		return "", fmt.Errorf("transcription provider %q not found in providers config (supported builtins: openai, mistral, local, whisper-cli)", providerName)
	}
}
