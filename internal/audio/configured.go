package audio

import (
	"fmt"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

// NewConfiguredVeniceProvider configures the Venice speech API root.
func NewConfiguredVeniceProvider(cfg *config.Config, apiKey string) (*VeniceProvider, error) {
	root, err := cfg.ProviderBaseURL("venice", cfg.Audio.Venice.BaseURL, veniceBaseURL)
	if err != nil {
		return nil, fmt.Errorf("audio.venice.base_url: %w", err)
	}
	p := NewVeniceProvider(apiKey)
	p.baseURL = root
	return p, nil
}

// NewConfiguredGeminiProvider configures the Gemini speech API root.
func NewConfiguredGeminiProvider(cfg *config.Config, apiKey string) (*GeminiProvider, error) {
	root, err := cfg.ProviderBaseURL("gemini", cfg.Audio.Gemini.BaseURL, strings.TrimSuffix(geminiBaseURL, "/models"))
	if err != nil {
		return nil, fmt.Errorf("audio.gemini.base_url: %w", err)
	}
	p := NewGeminiProvider(apiKey)
	p.baseURL = root + "/models"
	return p, nil
}

// NewConfiguredElevenLabsProvider configures the ElevenLabs speech API root.
func NewConfiguredElevenLabsProvider(cfg *config.Config, apiKey string) (*ElevenLabsProvider, error) {
	root, err := cfg.ProviderBaseURL("elevenlabs", cfg.Audio.ElevenLabs.BaseURL, elevenLabsBaseURL)
	if err != nil {
		return nil, fmt.Errorf("audio.elevenlabs.base_url: %w", err)
	}
	p := NewElevenLabsProvider(apiKey)
	p.baseURL = root
	return p, nil
}
