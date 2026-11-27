package proxy

import "testing"

// Distinct providers must never share a legend name: brand labels collapsed
// anthropic and claude-bin into "Claude".
func TestTimelineProviderLabelsUseConfiguredNames(t *testing.T) {
	tests := []struct {
		provider, route, wantKey, wantLabel string
	}{
		{"anthropic", "", "anthropic", "anthropic"},
		{"anthropic-work", "", "anthropic-work", "anthropic-work"},
		{"claude-bin", "", "claude-bin", "claude-bin"},
		{"", "openai", "openai", "openai"},
		{"", "", "", "Unknown provider"},
	}
	for _, tt := range tests {
		key, label := timelineGroupIdentity(MoneyRecord{Provider: tt.provider}, "", tt.route, "provider")
		if key != tt.wantKey || label != tt.wantLabel {
			t.Errorf("provider %q route %q = (%q, %q), want (%q, %q)", tt.provider, tt.route, key, label, tt.wantKey, tt.wantLabel)
		}
	}
}
