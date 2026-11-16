package image

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

// ImageResult contains the generated image and metadata
type ImageResult struct {
	Data     []byte // Image data (PNG/JPEG)
	MimeType string // "image/png", "image/jpeg", etc.
}

// GenerateRequest contains parameters for image generation
type GenerateRequest struct {
	Venice      *VeniceControls
	Prompt      string
	Size        string // Normalized resolution: "1K", "2K", "4K"; providers translate or ignore
	AspectRatio string // e.g. "1:1", "16:9", "9:16"; providers translate or ignore
	Quality     string // "auto", "low", "medium", "high", "xhigh", or "max"; provider/model support varies
	Background  string // "auto", "opaque", or "transparent"; provider/model support varies
	Debug       bool
	DebugRaw    bool
}

// InputImage represents a single input image for editing
type InputImage struct {
	URL  string // Remote URL passed through to capable providers
	Data []byte // Image data
	Path string // Path for MIME type detection
}

// EditRequest contains parameters for image editing
type EditRequest struct {
	Model       string // Explicit per-call edit model
	Prompt      string
	InputImages []InputImage // Input images for editing (supports multiple for some providers)
	Size        string       // Normalized resolution: "1K", "2K", "4K"; providers translate or ignore
	AspectRatio string       // e.g. "1:1", "16:9", "9:16"; providers translate or ignore
	Quality     string       // "auto", "low", "medium", "high", "xhigh", or "max"; provider/model support varies
	Background  string       // "auto", "opaque", or "transparent"; provider/model support varies
	Debug       bool
	DebugRaw    bool
}

// ImageProvider is the interface for image generation providers
type ImageProvider interface {
	// Name returns the provider name for logging
	Name() string

	// Generate creates a new image from a text prompt
	Generate(ctx context.Context, req GenerateRequest) (*ImageResult, error)

	// Edit modifies an existing image based on a prompt
	Edit(ctx context.Context, req EditRequest) (*ImageResult, error)

	// SupportsEdit returns true if the provider supports image editing
	SupportsEdit() bool

	// SupportsMultiImage returns true if the provider supports multiple input images
	SupportsMultiImage() bool
}

// NewImageProvider creates an image provider based on config
func NewImageProvider(cfg *config.Config, providerOverride string) (ImageProvider, error) {
	providerStr := providerOverride
	if providerStr == "" {
		providerStr = cfg.Image.Provider
	}
	if providerStr == "" {
		providerStr = config.DefaultImageProvider
	}

	// Parse provider:model syntax
	provider, model := parseImageProviderModel(providerStr)

	switch provider {
	case "gemini":
		apiKey, err := cfg.Image.GeminiKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.gemini.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not configured. Set environment variable or add to image.gemini.api_key in config")
		}
		if model == "" {
			model = cfg.Image.Gemini.Model
		}
		if err := ValidateSize(cfg.Image.Gemini.ImageSize); err != nil {
			return nil, fmt.Errorf("invalid image.gemini.image_size config: %w", err)
		}
		baseURL, err := cfg.ProviderBaseURL("gemini", cfg.Image.Gemini.BaseURL, strings.TrimSuffix(geminiBaseURL, "/models/"))
		if err != nil {
			return nil, fmt.Errorf("image.gemini.base_url: %w", err)
		}
		p := NewGeminiProvider(apiKey, model, cfg.Image.Gemini.ImageSize)
		p.baseURL = baseURL
		return p, nil

	case "openai":
		apiKey, err := cfg.Image.OpenAIKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.openai.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not configured. Set environment variable or add to image.openai.api_key in config")
		}
		if model == "" {
			model = cfg.Image.OpenAI.Model
		}
		endpoint, err := cfg.OpenAIEndpointWithBaseURL("image.openai.base_url", cfg.Image.OpenAI.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("OpenAI image endpoint: %w", err)
		}

		return NewOpenAIProviderWithEndpoint(apiKey, model, endpoint), nil

	case "chatgpt":
		if model == "" {
			model = cfg.Image.ChatGPT.Model
		}
		baseURL, err := cfg.ProviderBaseURL("chatgpt", cfg.Image.ChatGPT.BaseURL, strings.TrimSuffix(chatGPTImageGenerateEndpoint, "/images/generations"))
		if err != nil {
			return nil, fmt.Errorf("image.chatgpt.base_url: %w", err)
		}
		p, err := NewChatGPTProviderWithBaseURL(model, baseURL)
		if err != nil {
			return nil, err
		}
		p.generateEndpoint = baseURL + "/images/generations"
		p.editEndpoint = baseURL + "/images/edits"
		return p, nil

	case "xai", "grok":
		apiKey, err := cfg.Image.XAIKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.xai.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("XAI_API_KEY not configured. Set environment variable or add to image.xai.api_key in config")
		}
		if model == "" {
			model = cfg.Image.XAI.Model
		}
		baseURL, err := cfg.ProviderBaseURL("xai", cfg.Image.XAI.BaseURL, strings.TrimSuffix(xaiImageEndpoint, "/images/generations"))
		if err != nil {
			return nil, fmt.Errorf("image.xai.base_url: %w", err)
		}
		p := NewXAIProvider(apiKey, model)
		p.baseURL = baseURL
		return p, nil

	case "venice":
		apiKey, err := cfg.Image.VeniceKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.venice.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("VENICE_API_KEY not configured. Set environment variable or add to image.venice.api_key in config")
		}
		if model == "" {
			model = cfg.Image.Venice.Model
		}
		editModel := cfg.Image.Venice.EditModel
		baseURL, err := cfg.ProviderBaseURL("venice", cfg.Image.Venice.BaseURL, veniceBaseURL)
		if err != nil {
			return nil, fmt.Errorf("image.venice.base_url: %w", err)
		}
		p := NewVeniceProvider(apiKey, model, editModel, cfg.Image.Venice.Resolution)
		p.baseURL = baseURL
		return p, nil

	case "flux", "bfl":
		apiKey, err := cfg.Image.FluxKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.flux.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("BFL_API_KEY not configured. Set environment variable or add to image.flux.api_key in config")
		}
		baseURL, err := cfg.ProviderBaseURL("flux", cfg.Image.Flux.BaseURL, fluxBaseURL)
		if err != nil {
			return nil, fmt.Errorf("image.flux.base_url: %w", err)
		}
		p := NewFluxProvider(apiKey, model)
		p.baseURL = baseURL
		p.pinPolling = strings.TrimSpace(cfg.Image.Flux.BaseURL) != ""
		return p, nil

	case "openrouter":
		apiKey, err := cfg.Image.OpenRouterKey().Resolve()
		if err != nil {
			return nil, fmt.Errorf("image.openrouter.api_key: %w", err)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not configured. Set environment variable or add to image.openrouter.api_key in config")
		}
		if model == "" {
			model = cfg.Image.OpenRouter.Model
		}
		baseURL, err := cfg.ProviderBaseURL("openrouter", cfg.Image.OpenRouter.BaseURL, strings.TrimSuffix(openrouterImageEndpoint, "/chat/completions"))
		if err != nil {
			return nil, fmt.Errorf("image.openrouter.base_url: %w", err)
		}
		p := NewOpenRouterProvider(apiKey, model)
		p.baseURL = baseURL
		return p, nil

	case "debug":
		return NewDebugProvider(cfg.Image.Debug.Delay), nil

	default:
		return newConfiguredImageProvider(cfg, provider, model)
	}
}

// parseImageProviderModel parses "provider:model" or just "provider" from a string.
// Returns (provider, model). Model will be empty if not specified.
func parseImageProviderModel(s string) (string, string) {
	parts := strings.SplitN(s, ":", 2)
	provider := parts[0]
	model := ""
	if len(parts) == 2 {
		model = parts[1]
	}
	return provider, model
}

// ValidSizes are the normalized image size values accepted by the system.
var ValidSizes = []string{"1K", "2K", "4K"}

// ValidateAspectRatio checks for a positive W:H ratio.
func ValidateAspectRatio(aspectRatio string) error {
	if aspectRatio == "" {
		return nil
	}
	parts := strings.Split(aspectRatio, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid aspect ratio %q (expected W:H, for example 16:9)", aspectRatio)
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return fmt.Errorf("invalid aspect ratio %q (dimensions must be positive integers)", aspectRatio)
	}
	return nil
}

// ValidQualities are the image quality values accepted by the CLI and tool.
var ValidQualities = []string{"auto", "low", "medium", "high", "xhigh", "max"}

// ValidateQuality checks that quality is a recognized normalized value.
func ValidateQuality(quality string) error {
	return validateImageOption("quality", quality, ValidQualities)
}

// ValidBackgrounds are the image background modes accepted by the CLI and tool.
var ValidBackgrounds = []string{"auto", "opaque", "transparent"}

// ValidateBackground checks that background is a recognized mode.
func ValidateBackground(background string) error {
	return validateImageOption("background", background, ValidBackgrounds)
}

func validateImageOption(name, value string, valid []string) error {
	if value == "" {
		return nil
	}
	for _, candidate := range valid {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("invalid %s %q (valid: %s)", name, value, strings.Join(valid, ", "))
}

// ValidateSize checks that size is a recognized normalized value.
// Returns nil for empty string (no size requested). Returns an error for invalid values.
func ValidateSize(size string) error {
	if size == "" {
		return nil
	}
	for _, v := range ValidSizes {
		if size == v {
			return nil
		}
	}
	return fmt.Errorf("invalid size %q (valid: %s)", size, strings.Join(ValidSizes, ", "))
}
