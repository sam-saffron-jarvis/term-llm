package live

import (
	"fmt"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

// WebSocket/full-endpoint settings retain their existing URL contract and are
// resolved only when starting a session, never by readiness/UI bootstrap.
func resolveLiveEndpoint(path, raw, fallback string) (string, error) {
	value, err := config.Cred(raw).Resolve()
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return value, nil
}
