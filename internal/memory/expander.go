package memory

import "context"

// InsightsExpander wraps a Store and exposes a single Expand method for
// appending behavioral insights to the resolved system prompt. A nil
// *InsightsExpander is always a safe no-op — callers never need to nil-check
// before calling.
type InsightsExpander struct {
	store     *Store
	agent     string
	maxTokens int
}

// NewInsightsExpander returns an expander configured for the given agent.
// maxTokens is the token budget for the injected block (0 → default 500).
// Returns nil when store is nil — the nil receiver is safe to call.
func NewInsightsExpander(store *Store, agent string, maxTokens int) *InsightsExpander {
	if store == nil {
		return nil
	}
	if maxTokens <= 0 {
		maxTokens = 500
	}
	return &InsightsExpander{store: store, agent: agent, maxTokens: maxTokens}
}

// Expand returns the formatted insight block for the configured agent.
// Returns "" when disabled, no insights exist, or on any error.
func (e *InsightsExpander) Expand(ctx context.Context, _ string) string {
	if e == nil || e.store == nil {
		return ""
	}
	expanded, err := e.store.ExpandInsights(ctx, e.agent, "", e.maxTokens)
	if err != nil {
		return ""
	}
	return expanded
}
