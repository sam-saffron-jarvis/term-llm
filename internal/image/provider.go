package image

import (
	"context"
	"fmt"

	"github.com/samsaffron/term-llm/internal/config"
)

// ImageResult contains the generated image and metadata
type ImageResult struct {
	Data     []byte // Image data (PNG/JPEG)
	MimeType string // "image/png", "image/jpeg", etc.
}

// GenerateRequest contains parameters for image generation
type GenerateRequest struct {
	Prompt string
	Debug  bool
}

// EditRequest contains parameters for image editing
type EditRequest struct {
	Prompt     string
	InputImage []byte // Input image data
	InputPath  string // Path for MIME type detection
	Debug      bool
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
}

// NewImageProvider creates an image provider based on config
func NewImageProvider(cfg *config.Config, providerOverride string) (ImageProvider, error) {
	provider := providerOverride
	if provider == "" {
		provider = cfg.Image.Provider
	}
	if provider == "" {
		provider = "gemini" // default
	}

	switch provider {
	case "gemini":
		apiKey := cfg.Image.Gemini.APIKey
		if apiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not configured. Set environment variable or add to image.gemini.api_key in config")
		}
		return NewGeminiProvider(apiKey), nil

	case "openai":
		apiKey := cfg.Image.OpenAI.APIKey
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not configured. Set environment variable or add to image.openai.api_key in config")
		}
		return NewOpenAIProvider(apiKey), nil

	case "flux", "bfl":
		apiKey := cfg.Image.Flux.APIKey
		if apiKey == "" {
			return nil, fmt.Errorf("BFL_API_KEY not configured. Set environment variable or add to image.flux.api_key in config")
		}
		return NewFluxProvider(apiKey), nil

	default:
		return nil, fmt.Errorf("unknown image provider: %s (valid: gemini, openai, flux)", provider)
	}
}
