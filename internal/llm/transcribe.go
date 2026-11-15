package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
	rp "github.com/term-llm/term-llm/internal/llm/providers/responsesproviders"
	"github.com/term-llm/term-llm/internal/llm/providers/transcription"
	"github.com/term-llm/term-llm/internal/openaiapi"
)

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func transcribeAndTruncate(ctx context.Context, filePath string, opts transcription.Options) (string, error) {
	opts.Client = defaultHTTPClient
	transcript, err := transcription.TranscribeFile(ctx, filePath, opts)
	if err != nil {
		return "", err
	}
	if opts.Provider == "elevenlabs" && opts.Timestamps {
		return transcript, nil
	}
	return TruncateTranscriptIfImplausible(ctx, filePath, transcript), nil
}

func transcribeChatGPTAndTruncate(ctx context.Context, filePath, language, baseURL string) (string, error) {
	transcript, err := rp.TranscribeChatGPTWithBaseURL(ctx, filePath, language, baseURL)
	if err != nil {
		return "", err
	}
	return TruncateTranscriptIfImplausible(ctx, filePath, transcript), nil
}

// TranscribeWithConfig transcribes an audio file using the provider configured in cfg.
// providerOverride, if non-empty, overrides cfg.Transcription.Provider.
//
// Supported provider names: "openai" (default), "chatgpt" (ChatGPT OAuth), "mistral" (Voxtral), "venice", "elevenlabs", "local" (whisper.cpp server),
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
	getProviderConfig := func(name string) (*config.ProviderConfig, error) {
		providerCfg, err := cfg.GetResolvedProviderConfig(name)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		return providerCfg, nil
	}

	switch providerName {
	case "local":
		// whisper.cpp HTTP server — OpenAI-compatible endpoint, typically no auth required
		endpoint := "http://localhost:8080/inference"
		providerCfg, err := getProviderConfig("local_whisper")
		if err != nil {
			return "", err
		}
		if providerCfg != nil {
			baseURL, err := transcriptionProviderRoot("local_whisper", providerCfg)
			if err != nil {
				return "", err
			}
			if baseURL != "" {
				endpoint = baseURL + "/inference"
			}
		}
		return transcribeAndTruncate(ctx, filePath, transcription.Options{
			Endpoint: endpoint,
			Model:    modelOverride,
			Language: language,
		})

	case "mistral":
		mistralCfg, err := getProviderConfig("mistral")
		if err != nil {
			return "", err
		}
		if mistralCfg == nil {
			mistralCfg = &config.ProviderConfig{}
		}
		apiKey, err := transcriptionProviderKey(mistralCfg)
		if err != nil {
			return "", err
		}
		if apiKey == "" {
			apiKey = os.Getenv("MISTRAL_API_KEY")
		}
		if apiKey == "" {
			return "", fmt.Errorf("transcription provider %q has no API key configured (providers.mistral.api_key or MISTRAL_API_KEY)", providerName)
		}
		endpoint := "https://api.mistral.ai/v1/audio/transcriptions"
		baseURL, err := transcriptionProviderRoot("mistral", mistralCfg)
		if err != nil {
			return "", err
		}
		if baseURL != "" {
			endpoint = strings.TrimRight(baseURL, "/") + "/audio/transcriptions"
		}
		model := modelOverride
		if model == "" {
			model = config.DefaultTranscriptionMistralModel
		}
		return transcribeAndTruncate(ctx, filePath, transcription.Options{
			APIKey:   apiKey,
			Endpoint: endpoint,
			Model:    model,
			Language: language,
		})

	case "venice":
		apiKey, err := config.ResolveFirstCredential(
			cfg.Transcription.VeniceKey(),
			cfg.Audio.VeniceKey(),
			cfg.Image.VeniceKey(),
		)
		if err != nil {
			return "", fmt.Errorf("venice transcription credentials: %w", err)
		}
		if apiKey == "" {
			apiKey, err = transcriptionProviderKey(cfg.GetProviderConfig("venice"))
			if err != nil {
				return "", fmt.Errorf("venice transcription credentials: %w", err)
			}
		}
		if apiKey == "" {
			return "", fmt.Errorf("transcription provider %q has no API key configured (transcription.venice.api_key, VENICE_API_KEY, audio.venice.api_key, image.venice.api_key, or providers.venice.api_key)", providerName)
		}
		baseURL, err := transcriptionFeatureRoot(cfg, "venice", cfg.Transcription.Venice.BaseURL, "https://api.venice.ai/api/v1")
		if err != nil {
			return "", fmt.Errorf("venice transcription base URL: %w", err)
		}
		endpoint := baseURL + "/audio/transcriptions"
		model := firstNonEmptyString(modelOverride, cfg.Transcription.Venice.Model, config.DefaultTranscriptionVeniceModel)
		return transcribeAndTruncate(ctx, filePath, transcription.Options{
			APIKey:     apiKey,
			Endpoint:   endpoint,
			Model:      model,
			Language:   language,
			Provider:   "venice",
			Timestamps: cfg.Transcription.Timestamps,
		})

	case "elevenlabs":
		apiKey, err := config.ResolveFirstCredential(
			cfg.Transcription.ElevenLabsKey(),
			cfg.Audio.ElevenLabsKey(),
		)
		if err != nil {
			return "", fmt.Errorf("elevenlabs transcription credentials: %w", err)
		}
		if apiKey == "" {
			apiKey, err = transcriptionProviderKey(cfg.GetProviderConfig("elevenlabs"))
			if err != nil {
				return "", fmt.Errorf("elevenlabs transcription credentials: %w", err)
			}
		}
		if apiKey == "" {
			return "", fmt.Errorf("transcription provider %q has no API key configured (transcription.elevenlabs.api_key, ELEVENLABS_API_KEY, XI_API_KEY, audio.elevenlabs.api_key, or providers.elevenlabs.api_key)", providerName)
		}
		baseURL, err := transcriptionFeatureRoot(cfg, "elevenlabs", cfg.Transcription.ElevenLabs.BaseURL, "https://api.elevenlabs.io/v1")
		if err != nil {
			return "", fmt.Errorf("elevenlabs transcription base URL: %w", err)
		}
		endpoint := baseURL + "/speech-to-text"
		model := firstNonEmptyString(modelOverride, cfg.Transcription.ElevenLabs.Model, config.DefaultTranscriptionElevenLabsModel)
		return transcribeAndTruncate(ctx, filePath, transcription.Options{
			APIKey:     apiKey,
			Endpoint:   endpoint,
			Model:      model,
			Language:   language,
			Provider:   "elevenlabs",
			Timestamps: cfg.Transcription.Timestamps,
		})

	case "chatgpt":
		// ChatGPT OAuth session; the backend chooses the model, so
		// transcription.model and timestamps are not applicable.
		baseURL, err := config.ResolveBaseURL("transcription.chatgpt.base_url", cfg.Transcription.ChatGPT.BaseURL, "https://chatgpt.com/backend-api")
		if err != nil {
			return "", fmt.Errorf("chatgpt transcription base URL: %w", err)
		}
		return transcribeChatGPTAndTruncate(ctx, filePath, language, baseURL)

	case "openai":
		// Named provider entry takes precedence over env var
		openAIProvider, err := getProviderConfig(string(config.ProviderTypeOpenAI))
		if err != nil {
			return "", err
		}
		if openAIProvider != nil {
			key, err := transcriptionProviderKey(openAIProvider)
			if err != nil {
				return "", err
			}
			openAIProvider.ResolvedAPIKey = key
			if openAIProvider.ResolvedURL == "" && strings.TrimSpace(openAIProvider.URL) == "" && strings.TrimSpace(openAIProvider.BaseURL) != "" {
				if _, err := resolveTranscriptionRoot("providers.openai.base_url", openAIProvider.BaseURL); err != nil {
					return "", err
				}
			}
			if openAIProvider.ResolvedURL == "" && openAIProvider.URL != "" {
				root, err := resolveTranscriptionRoot("providers.openai.url", openAIProvider.URL)
				if err != nil {
					return "", err
				}
				openAIProvider.ResolvedURL = root
			}
		}
		endpoint, apiKey, err := openAITranscriptionTarget(cfg, openAIProvider)
		if err != nil {
			return "", err
		}
		if apiKey == "" {
			return "", fmt.Errorf("transcription is not configured — set providers.openai.api_key or configure a transcription provider in config")
		}
		return transcribeAndTruncate(ctx, filePath, transcription.Options{
			APIKey:   apiKey,
			Endpoint: endpoint.URL("audio/transcriptions"),
			Headers:  endpoint.HeaderMap(),
			Model:    modelOverride,
			Language: language,
		})

	default:
		// Try looking up by name in providers map (allows any openai_compatible provider)
		providerCfg, err := getProviderConfig(providerName)
		if err != nil {
			return "", err
		}
		if providerCfg != nil {
			apiKey, err := transcriptionProviderKey(providerCfg)
			if err != nil {
				return "", err
			}
			if apiKey == "" {
				return "", fmt.Errorf("transcription provider %q has no API key configured", providerName)
			}
			base, err := transcriptionProviderRoot(providerName, providerCfg)
			if err != nil {
				return "", err
			}
			if base == "" {
				return "", fmt.Errorf("transcription provider %q requires a non-empty url or base_url", providerName)
			}
			endpoint := base + "/audio/transcriptions"
			return transcribeAndTruncate(ctx, filePath, transcription.Options{
				APIKey:   apiKey,
				Endpoint: endpoint,
				Model:    modelOverride,
				Language: language,
			})
		}
		return "", fmt.Errorf("transcription provider %q not found in providers config (supported builtins: openai, chatgpt, mistral, venice, elevenlabs, local, whisper-cli)", providerName)
	}
}

// openAITranscriptionTarget picks the endpoint and API key for
// transcription.provider: openai. A native providers.openai entry contributes
// its key, base_url and headers, with OPENAI_* environment fallbacks. An entry
// named openai of another type keeps its legacy routing: its own key goes only
// to its own endpoint, and no OpenAI organization/project headers are added.
func openAITranscriptionTarget(cfg *config.Config, entry *config.ProviderConfig) (openaiapi.Endpoint, string, error) {
	name := string(config.ProviderTypeOpenAI)
	if entry != nil && config.InferProviderType(name, entry.Type) != config.ProviderTypeOpenAI {
		endpoint := openaiapi.Public()
		if entry.ResolvedAPIKey == "" {
			return endpoint, os.Getenv("OPENAI_API_KEY"), nil
		}
		base, err := transcriptionProviderRoot(name, entry)
		if err != nil {
			return openaiapi.Endpoint{}, "", err
		}
		if base == "" {
			return openaiapi.Endpoint{}, "", fmt.Errorf("transcription provider %q requires a non-empty url or base_url", name)
		}
		endpoint.BaseURL = base
		return endpoint, entry.ResolvedAPIKey, nil
	}
	endpoint, err := cfg.OpenAIEndpoint()
	if err != nil {
		return openaiapi.Endpoint{}, "", fmt.Errorf("OpenAI transcription endpoint: %w", err)
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if entry != nil {
		if entry.ResolvedURL != "" {
			endpoint.BaseURL = entry.ResolvedURL
		}
		if entry.ResolvedAPIKey != "" {
			apiKey = entry.ResolvedAPIKey
		}
	}
	return endpoint, apiKey, nil
}

// transcriptionProviderRoot treats the legacy url as an API root, just as
// base_url, and appends the transcription operation at the call site.
func transcriptionProviderRoot(name string, entry *config.ProviderConfig) (string, error) {
	if entry == nil {
		return "", nil
	}
	root, err := resolveTranscriptionRoot("providers."+name+".url", firstNonEmptyString(entry.ResolvedURL, entry.URL))
	if err != nil || root != "" {
		return root, err
	}
	return resolveTranscriptionRoot("providers."+name+".base_url", entry.BaseURL)
}

func transcriptionProviderKey(entry *config.ProviderConfig) (string, error) {
	if entry == nil {
		return "", nil
	}
	if entry.ResolvedAPIKey != "" {
		return entry.ResolvedAPIKey, nil
	}
	return config.Cred(entry.APIKey).Resolve()
}

func transcriptionFeatureRoot(cfg *config.Config, name, override, fallback string) (string, error) {
	root, err := resolveTranscriptionRoot("transcription."+name+".base_url", override)
	if err != nil || root != "" {
		return root, err
	}
	entry := cfg.GetProviderConfig(name)
	if entry != nil {
		// A legacy url on the named transcription entry historically supplied
		// the endpoint even when that entry had an explicit compatible type.
		if legacy := firstNonEmptyString(entry.ResolvedURL, entry.URL); legacy != "" {
			root, err = resolveTranscriptionRoot("providers."+name+".url", legacy)
			if err != nil || root != "" {
				return root, err
			}
		}
		if entry.Type == "" || string(config.InferProviderType(name, entry.Type)) == name {
			root, err = transcriptionProviderRoot(name, entry)
			if err != nil || root != "" {
				return root, err
			}
		}
	}
	return fallback, nil
}

// Explicit but empty lazy endpoints must not fall through to a public host.
func resolveTranscriptionRoot(path, raw string) (string, error) {
	root, err := config.ResolveBaseURL(path, raw, "")
	if err == nil && root == "" && strings.TrimSpace(raw) != "" {
		return "", fmt.Errorf("%s resolved to an empty endpoint", path)
	}
	return root, err
}
