package proxyclient

import (
	"errors"
	"os"
)

// ValidateProfiling rejects diagnostics that could expose proxy credentials.
func ValidateProfiling(debugRaw bool, cpuProfile, memProfile, pprof string) error {
	if debugRaw || cpuProfile != "" || memProfile != "" || pprof != "" || os.Getenv("TERM_LLM_PPROF") != "" {
		return errors.New("standalone credential proxy refuses --debug-raw, CPU/heap profiles and TERM_LLM_PPROF/--pprof")
	}
	return nil
}
