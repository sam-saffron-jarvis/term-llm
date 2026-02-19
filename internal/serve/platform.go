package serve

import (
	"context"
	"time"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm"
)

// Settings holds per-platform runtime settings derived from CLI flags and config.
type Settings struct {
	SystemPrompt string
	IdleTimeout  time.Duration
	MaxTurns     int
	Search       bool
	// NewEngine creates a fresh LLM engine instance for a new session.
	// Called once per chat session.
	NewEngine func() (*llm.Engine, error)
}

// Platform is the interface implemented by each messaging platform adapter.
type Platform interface {
	// Name returns the platform identifier (e.g. "telegram").
	Name() string
	// NeedsSetup returns true when required configuration is missing.
	NeedsSetup() bool
	// RunSetup runs an interactive wizard to collect and persist configuration.
	RunSetup() error
	// Run starts the platform's message loop, blocking until ctx is cancelled.
	Run(ctx context.Context, cfg *config.Config, settings Settings) error
}
