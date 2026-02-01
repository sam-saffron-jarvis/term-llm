package ui

import (
	"testing"
)

func TestStripLeadingBlankLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"no newline", "hello", "hello"},
		{"blank first line", "   \nworld", "world"},
		{"content first line", "hello\nworld", "hello\nworld"},
		{"only blank", "   \n", ""},
		{"multiple blank lines", "   \n   \nworld", "   \nworld"},
		{"three blank lines", "\n  \n\nworld", "  \n\nworld"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripLeadingBlankLine(tt.input)
			if got != tt.expected {
				t.Errorf("stripLeadingBlankLine(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
