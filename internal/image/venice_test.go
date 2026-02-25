package image

import "testing"

func TestNewVeniceProviderDefaults(t *testing.T) {
	provider := NewVeniceProvider("api-key", "", "")
	if provider.model != veniceDefaultModel {
		t.Errorf("expected default model %q, got %q", veniceDefaultModel, provider.model)
	}
	if provider.resolution != veniceDefaultResolution {
		t.Errorf("expected default resolution %q, got %q", veniceDefaultResolution, provider.resolution)
	}
}

func TestVeniceResolutionDimensions(t *testing.T) {
	tests := []struct {
		name       string
		resolution string
		width      int
		height     int
	}{
		{name: "1K", resolution: "1K", width: 1024, height: 1024},
		{name: "2K", resolution: "2K", width: 2048, height: 2048},
		{name: "4K", resolution: "4K", width: 4096, height: 4096},
		{name: "default", resolution: "", width: 2048, height: 2048},
		{name: "unknown", resolution: "weird", width: 2048, height: 2048},
		{name: "lowercase", resolution: "2k", width: 2048, height: 2048},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := veniceResolutionDimensions(tt.resolution)
			if width != tt.width || height != tt.height {
				t.Errorf("veniceResolutionDimensions(%q) = %dx%d, want %dx%d", tt.resolution, width, height, tt.width, tt.height)
			}
		})
	}
}
