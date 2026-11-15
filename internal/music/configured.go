package music

import (
	"fmt"
	"github.com/term-llm/term-llm/internal/config"
)

// NewConfiguredVeniceProvider configures the Venice music API root.
func NewConfiguredVeniceProvider(cfg *config.Config, apiKey string) (*VeniceProvider, error) {
	root, err := cfg.ProviderBaseURL("venice", cfg.Music.Venice.BaseURL, veniceBaseURL)
	if err != nil {
		return nil, fmt.Errorf("music.venice.base_url: %w", err)
	}
	p := NewVeniceProvider(apiKey)
	p.baseURL = root
	return p, nil
}

// NewConfiguredElevenLabsProvider configures the ElevenLabs music API root.
func NewConfiguredElevenLabsProvider(cfg *config.Config, apiKey string) (*ElevenLabsProvider, error) {
	root, err := cfg.ProviderBaseURL("elevenlabs", cfg.Music.ElevenLabs.BaseURL, elevenLabsBaseURL)
	if err != nil {
		return nil, fmt.Errorf("music.elevenlabs.base_url: %w", err)
	}
	p := NewElevenLabsProvider(apiKey)
	p.baseURL = root
	return p, nil
}
