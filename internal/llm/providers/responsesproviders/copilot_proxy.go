package responsesproviders

import (
	"github.com/term-llm/term-llm/internal/credentials"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

// NewCopilotProviderWithBaseURL selects delegated auth before the native
// constructor can read or prompt for a local GitHub login.
func NewCopilotProviderWithBaseURL(model, baseURL string) (*CopilotProvider, error) {
	if binding, ok := proxyclient.Lookup(baseURL); ok && binding.Kind == "copilot" {
		creds, err := credentials.GetCopilotCredentialsForEndpoint(baseURL)
		if err != nil {
			return nil, err
		}
		provider := NewCopilotProviderWithCreds(creds, model)
		provider.SetBaseURL(baseURL)
		return provider, nil
	}
	provider, err := NewCopilotProvider(model)
	if err != nil {
		return nil, err
	}
	provider.SetBaseURL(baseURL)
	return provider, nil
}
