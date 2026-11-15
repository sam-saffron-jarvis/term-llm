package video

import (
	"fmt"
	"github.com/term-llm/term-llm/internal/config"
)

// NewConfiguredVeniceProvider configures the Venice video API root.
func NewConfiguredVeniceProvider(cfg *config.Config, apiKey string) (*VeniceProvider, error) {
	root, err := cfg.ProviderBaseURL("venice", cfg.Image.Venice.BaseURL, veniceBaseURL)
	if err != nil {
		return nil, fmt.Errorf("image.venice.base_url: %w", err)
	}
	p := NewVeniceProvider(apiKey)
	p.baseURL = root
	return p, nil
}
