package llm

import (
	"context"
	"strings"
)

const veniceBaseURL = "https://api.venice.ai/api/v1"

type VeniceProvider struct {
	*OpenAICompatProvider
}

func NewVeniceProvider(apiKey, model string) *VeniceProvider {
	if model == "" {
		model = "venice-uncensored"
	}
	return &VeniceProvider{OpenAICompatProvider: NewOpenAICompatProvider(veniceBaseURL, apiKey, model, "Venice")}
}

func (p *VeniceProvider) Capabilities() Capabilities {
	return Capabilities{
		NativeWebSearch:    true,
		NativeWebFetch:     false,
		ToolCalls:          true,
		SupportsToolChoice: true,
	}
}

func (p *VeniceProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	req.ParallelToolCalls = false
	if req.Search {
		req.Model = appendVeniceModelSuffix(chooseModel(req.Model, p.model), "enable_web_search=on")
	}
	return p.OpenAICompatProvider.Stream(ctx, req)
}

func appendVeniceModelSuffix(model, suffix string) string {
	if model == "" || suffix == "" {
		return model
	}
	if strings.Contains(model, ":") {
		return model + "&" + suffix
	}
	return model + ":" + suffix
}
