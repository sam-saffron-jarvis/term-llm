package image

import (
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
)

func TestNewImageProviderGeminiModelPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		override  string // provider override string (e.g. "gemini:custom-model")
		cfgModel  string // image.gemini.model config value
		wantModel string
	}{
		{"override model wins", "gemini:custom-model", "config-model", "custom-model"},
		{"config model used when no override", "gemini", "config-model", "config-model"},
		{"default model when both empty", "gemini", "", geminiDefaultModel},
		{"empty override falls back to config", "", "config-model", "config-model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Image.Gemini.APIKey = "test-key"
			cfg.Image.Gemini.Model = tt.cfgModel

			p, err := NewImageProvider(cfg, tt.override)
			if err != nil {
				t.Fatalf("NewImageProvider failed: %v", err)
			}

			gp, ok := p.(*GeminiProvider)
			if !ok {
				t.Fatalf("expected *GeminiProvider, got %T", p)
			}
			if gp.model != tt.wantModel {
				t.Errorf("model=%q, want %q", gp.model, tt.wantModel)
			}
		})
	}
}

func TestNewImageProviderGeminiSizePrecedence(t *testing.T) {
	cfg := &config.Config{}
	cfg.Image.Gemini.APIKey = "test-key"
	cfg.Image.Gemini.ImageSize = "2K"

	p, err := NewImageProvider(cfg, "gemini")
	if err != nil {
		t.Fatalf("NewImageProvider failed: %v", err)
	}

	gp := p.(*GeminiProvider)
	if gp.defaultSize != "2K" {
		t.Errorf("defaultSize=%q, want %q", gp.defaultSize, "2K")
	}
}

func TestValidateSize(t *testing.T) {
	tests := []struct {
		size    string
		wantErr bool
	}{
		{"", false},
		{"1K", false},
		{"2K", false},
		{"4K", false},
		{"2k", true},
		{"512px", true},
		{"2048", true},
		{"large", true},
	}
	for _, tt := range tests {
		name := tt.size
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			err := ValidateSize(tt.size)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSize(%q) error=%v, wantErr=%v", tt.size, err, tt.wantErr)
			}
		})
	}
}
