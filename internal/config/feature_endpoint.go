package config

import (
	"github.com/term-llm/term-llm/internal/openaiapi"
)

// OpenAIEndpointWithBaseURL resolves a feature override before the shared
// Platform endpoint. A configured higher-priority root never resolves an
// unused shared base_url; shared headers still scope the same API-key account.
func (c *Config) OpenAIEndpointWithBaseURL(path, raw string) (openaiapi.Endpoint, error) {
	base, err := ResolveBaseURL(path, raw, "")
	if err != nil {
		return openaiapi.Endpoint{}, err
	}
	if base == "" {
		return c.OpenAIEndpoint()
	}
	var headers map[string]string
	if pc := c.GetProviderConfig("openai"); pc != nil && InferProviderType("openai", pc.Type) == ProviderTypeOpenAI {
		headers, err = ResolveHeaderValues(pc.Headers)
		if err != nil {
			return openaiapi.Endpoint{}, err
		}
	}
	return openaiapi.Resolve(openaiapi.Endpoint{BaseURL: base, Headers: headers}), nil
}
