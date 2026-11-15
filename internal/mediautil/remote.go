package mediautil

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// IsRemoteReference identifies URL-looking inputs, including unsupported schemes.
// Such inputs must not accidentally be interpreted as local file paths.
func IsRemoteReference(value string) bool {
	return strings.Contains(value, "://") || strings.HasPrefix(value, "http:") || strings.HasPrefix(value, "https:") || strings.HasPrefix(value, "data:")
}

// ValidateRemoteURL validates pass-through references without fetching them.
// Errors deliberately omit the reference (which may contain signed secrets).
func ValidateRemoteURL(value string) error {
	if len(value) == 0 || len(value) > 8192 || strings.Contains(value, "\\") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return fmt.Errorf("invalid remote media URL")
	}
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(value, "#") {
		return fmt.Errorf("remote media URL must be HTTP(S), with a host and without credentials or fragments")
	}
	return nil
}

// DiagnosticReference retains useful reference identity but never signed query
// tokens, userinfo or fragments in persisted/output diagnostics.
func DiagnosticReference(value string) string {
	if !IsRemoteReference(value) {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "[remote reference]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
