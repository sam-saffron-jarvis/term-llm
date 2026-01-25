package ui

import "testing"

func TestExtractAgentName(t *testing.T) {
	tests := []struct {
		name     string
		toolInfo string
		want     string
	}{
		{
			name:     "empty",
			toolInfo: "",
			want:     "",
		},
		{
			name:     "with parens and @",
			toolInfo: "(@reviewer: Analyze the codebase...)",
			want:     "reviewer",
		},
		{
			name:     "with @ no parens",
			toolInfo: "@reviewer: Analyze the codebase...",
			want:     "reviewer",
		},
		{
			name:     "no @ or parens",
			toolInfo: "reviewer: Analyze the codebase...",
			want:     "reviewer",
		},
		{
			name:     "just name",
			toolInfo: "reviewer",
			want:     "reviewer",
		},
		{
			name:     "name with space",
			toolInfo: "reviewer some prompt",
			want:     "reviewer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAgentName(tt.toolInfo)
			if got != tt.want {
				t.Errorf("extractAgentName(%q) = %q, want %q", tt.toolInfo, got, tt.want)
			}
		})
	}
}
