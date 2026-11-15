package pprof

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolvePort returns the port for the pprof server.
// Returns -1 if pprof should not be started.
// Returns 0 for random port, or a specific port number.
// Returns an error for invalid port values.
func ResolvePort(flag, environment string) (int, error) {
	// Check flag first (--pprof or --pprof=PORT)
	if flag != "" {
		port, err := strconv.Atoi(flag)
		if err != nil {
			return 0, fmt.Errorf("invalid --pprof value %q: must be a port number", flag)
		}
		if err := validatePort(port); err != nil {
			return 0, fmt.Errorf("invalid --pprof port: %w", err)
		}
		return port, nil
	}

	// Check environment variable
	if envPort := environment; envPort != "" {
		// Treat "1" or "true" as "enable with random port"
		if envPort == "1" || strings.EqualFold(envPort, "true") {
			return 0, nil
		}
		// Otherwise parse as explicit port number
		port, err := strconv.Atoi(envPort)
		if err != nil {
			return 0, fmt.Errorf("invalid TERM_LLM_PPROF value %q: must be a port number, '1', or 'true'", envPort)
		}
		if err := validatePort(port); err != nil {
			return 0, fmt.Errorf("invalid TERM_LLM_PPROF port: %w", err)
		}
		return port, nil
	}

	return -1, nil // pprof disabled
}

// validatePort checks that a port number is valid.
// Port 0 means "random port", ports 1-65535 are valid explicit ports.
func validatePort(port int) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("port %d out of range (must be 0-65535)", port)
	}
	return nil
}
