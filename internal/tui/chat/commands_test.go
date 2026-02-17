package chat

import (
	"testing"
)

func TestAllCommandsIncludesCompact(t *testing.T) {
	commands := AllCommands()
	found := false
	for _, cmd := range commands {
		if cmd.Name == "compact" {
			found = true
			if cmd.Usage != "/compact" {
				t.Errorf("compact usage = %q, want /compact", cmd.Usage)
			}
			break
		}
	}
	if !found {
		t.Error("AllCommands() should include 'compact' command")
	}
}

func TestFilterCommandsMatchesCompact(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"compact", true},
		{"comp", true},
		{"compa", true},
		{"xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results := FilterCommands(tt.query)
			found := false
			for _, cmd := range results {
				if cmd.Name == "compact" {
					found = true
					break
				}
			}
			if found != tt.want {
				t.Errorf("FilterCommands(%q) found compact = %v, want %v", tt.query, found, tt.want)
			}
		})
	}
}
