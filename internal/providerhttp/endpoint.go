package providerhttp

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// EnvBaseURL selects an explicit environment API root or fallback. It is for
// ancillary clients that do not load application configuration (for example
// update discovery). No credential or returned download URL is rewritten.
// API roots retain proxy prefixes and must not contain userinfo/query/fragment.
func EnvBaseURL(env, fallback string) (string, error) {
	value := strings.TrimSpace(os.Getenv(env))
	if value == "" {
		value = fallback
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") {
		return "", fmt.Errorf("%s must be an HTTP(S) API root without credentials, query or fragment", env)
	}
	return strings.TrimRight(value, "/"), nil
}
