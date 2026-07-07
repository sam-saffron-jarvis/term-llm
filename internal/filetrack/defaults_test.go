package filetrack

import (
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestFileTrackingDefaultsMatchConfig(t *testing.T) {
	if DefaultMaxFileBytes != config.DefaultFileTrackingMaxFileBytes {
		t.Fatalf("DefaultMaxFileBytes = %d, want %d", DefaultMaxFileBytes, config.DefaultFileTrackingMaxFileBytes)
	}
	if DefaultMaxSessionBytes != config.DefaultFileTrackingMaxSessionBytes {
		t.Fatalf("DefaultMaxSessionBytes = %d, want %d", DefaultMaxSessionBytes, config.DefaultFileTrackingMaxSessionBytes)
	}
	if DefaultMaxTotalBytes != config.DefaultFileTrackingMaxTotalBytes {
		t.Fatalf("DefaultMaxTotalBytes = %d, want %d", DefaultMaxTotalBytes, config.DefaultFileTrackingMaxTotalBytes)
	}
}
