package venice

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/term-llm/term-llm/internal/providerhttp"
)

// StatusError bounds diagnostics and redacts credentials if a gateway echoes
// them. It preserves HTTP status/header metadata without exposing response dumps.
func StatusError(resp *http.Response, body []byte, key string) *providerhttp.StatusError {
	return SanitizeStatusError(providerhttp.NewStatusError("Venice", resp, body), key)
}

func SanitizeStatusError(err *providerhttp.StatusError, key string) *providerhttp.StatusError {
	safe := *err
	body := safe.Body
	if key != "" {
		body = strings.ReplaceAll(body, key, "[redacted]")
		encoded, _ := json.Marshal(key)
		if len(encoded) > 2 {
			body = strings.ReplaceAll(body, string(encoded[1:len(encoded)-1]), "[redacted]")
		}
	}
	if len(body) > 1024 {
		body = body[:1024] + " [truncated]"
	}
	safe.Body = body
	return &safe
}
