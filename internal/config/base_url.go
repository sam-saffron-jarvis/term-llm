package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveBaseURL resolves a lazy API root at point of use. An empty resolution
// selects fallback. API roots include any version/proxy prefix, but never a
// query, fragment or userinfo; request paths are appended, not URL-resolved.
func ResolveBaseURL(path, raw, fallback string) (string, error) {
	value, err := Cred(raw).Resolve()
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") {
		return "", fmt.Errorf("%s must be an HTTP(S) API root without credentials, query or fragment", path)
	}
	return strings.TrimRight(value, "/"), nil
}

// ProviderBaseURL selects a feature override, then the same-named native
// provider's API root, then fallback. Only endpoint fields are resolved: an
// unused inference key or header must not unlock a vault for a media request.
// A custom provider of another type never supplies a native feature's root.
func (c *Config) ProviderBaseURL(name, raw, fallback string) (string, error) {
	value, err := ResolveBaseURL(name+".base_url", raw, "")
	if err != nil || value != "" {
		return value, err
	}
	if pc := c.GetProviderConfig(name); pc != nil && string(InferProviderType(name, pc.Type)) == name {
		return ResolveBaseURL("providers."+name+".base_url", pc.BaseURL, fallback)
	}
	return ResolveBaseURL(name+".base_url", "", fallback)
}
