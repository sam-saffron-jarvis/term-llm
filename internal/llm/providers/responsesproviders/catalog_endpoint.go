package responsesproviders

import "github.com/term-llm/term-llm/internal/providerhttp"

// Only the built-in catalog location is configurable here. Explicit gateway
// providers suppress this optional catalog entirely, and injected catalog URLs
// keep their full-endpoint contract.
func configuredOpenCodeCatalogURL(endpoint string) (string, error) {
	if endpoint != opencodeGoCatalogURL {
		return endpoint, nil
	}
	root, err := providerhttp.EnvBaseURL("OPENCODE_CATALOG_BASE_URL", "https://models.opencode.ai")
	if err != nil {
		return "", err
	}
	return root + "/api.json", nil
}
