package embedding

import (
	"math"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{-1, 0, 0},
			expected: -1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "similar vectors",
			a:        []float64{1, 1, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0 / math.Sqrt(2),
		},
		{
			name:     "zero vector",
			a:        []float64{0, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 0.0,
		},
		{
			name:     "empty vectors",
			a:        []float64{},
			b:        []float64{},
			expected: 0.0,
		},
		{
			name:     "mismatched lengths",
			a:        []float64{1, 2},
			b:        []float64{1, 2, 3},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CosineSimilarity(tt.a, tt.b)
			if math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestParseProviderModel(t *testing.T) {
	tests := []struct {
		input    string
		provider string
		model    string
	}{
		{"openai", "openai", ""},
		{"openai:text-embedding-3-large", "openai", "text-embedding-3-large"},
		{"gemini", "gemini", ""},
		{"gemini:gemini-embedding-001", "gemini", "gemini-embedding-001"},
		{"ollama:nomic-embed-text", "ollama", "nomic-embed-text"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p, m := parseProviderModel(tt.input)
			if p != tt.provider {
				t.Errorf("parseProviderModel(%q) provider = %q, want %q", tt.input, p, tt.provider)
			}
			if m != tt.model {
				t.Errorf("parseProviderModel(%q) model = %q, want %q", tt.input, m, tt.model)
			}
		})
	}
}

func TestInferEmbeddingProvider(t *testing.T) {
	tests := []struct {
		name            string
		defaultProvider string
		hasVoyageKey    bool
		expected        string
	}{
		{"openai provider", "openai", false, "openai"},
		{"gemini provider", "gemini", false, "gemini"},
		{"gemini-cli provider", "gemini-cli", false, "gemini"},
		{"anthropic defaults to gemini", "anthropic", false, "gemini"},
		{"anthropic with voyage key", "anthropic", true, "voyage"},
		{"claude-bin defaults to gemini", "claude-bin", false, "gemini"},
		{"claude-bin with voyage key", "claude-bin", true, "voyage"},
		{"unknown defaults to gemini", "something", false, "gemini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				DefaultProvider: tt.defaultProvider,
			}
			if tt.hasVoyageKey {
				cfg.Embed.Voyage.APIKey = "test-key"
			}
			result := inferEmbeddingProvider(cfg)
			if result != tt.expected {
				t.Errorf("inferEmbeddingProvider(%q) = %q, want %q", tt.defaultProvider, result, tt.expected)
			}
		})
	}
}
