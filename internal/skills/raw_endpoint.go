package skills

import (
	"strings"

	"github.com/term-llm/term-llm/internal/providerhttp"
)

// RawGitHubURL constructs a skill's raw-content URL from a GitHub repository
// shorthand using the explicit content root. Already supplied resource URLs
// must be fetched directly, not passed through this constructor.
func RawGitHubURL(repository string) (string, error) {
	canonical := constructRawGitHubURL(repository)
	if canonical == "" {
		return "", nil
	}
	base, err := providerhttp.EnvBaseURL("TERM_LLM_GITHUB_RAW_BASE_URL", githubRawBase)
	if err != nil {
		return "", err
	}
	return base + strings.TrimPrefix(canonical, githubRawBase), nil
}
