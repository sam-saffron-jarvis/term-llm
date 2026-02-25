package image

import "testing"

func TestNewVeniceProviderDefaults(t *testing.T) {
	provider := NewVeniceProvider("api-key", "", "", "")
	if provider.model != veniceDefaultModel {
		t.Errorf("expected default model %q, got %q", veniceDefaultModel, provider.model)
	}
	if provider.resolution != veniceDefaultResolution {
		t.Errorf("expected default resolution %q, got %q", veniceDefaultResolution, provider.resolution)
	}
}

func TestNewVeniceProviderCustom(t *testing.T) {
	provider := NewVeniceProvider("key", "flux-2-max", "", "4K")
	if provider.model != "flux-2-max" {
		t.Errorf("expected model %q, got %q", "flux-2-max", provider.model)
	}
	if provider.resolution != "4K" {
		t.Errorf("expected resolution %q, got %q", "4K", provider.resolution)
	}
}

func TestVeniceEditModel(t *testing.T) {
	tests := []struct {
		model     string
		editModel string
		want      string
	}{
		{"nano-banana-pro", "", "nano-banana-pro-edit"},        // auto-suffix
		{"flux-2-max", "", "flux-2-max-edit"},                  // auto-suffix
		{"seedream-v4", "", "seedream-v4-edit"},                // auto-suffix
		{"qwen-edit", "", "qwen-edit"},                         // already has suffix
		{"nano-banana-pro-edit", "", "nano-banana-pro-edit"},   // already has suffix
		{"nano-banana-pro", "qwen-edit", "qwen-edit"},          // explicit override
		{"flux-2-max", "seedream-v4-edit", "seedream-v4-edit"}, // explicit override
	}
	for _, tt := range tests {
		p := NewVeniceProvider("key", tt.model, tt.editModel, "")
		got := p.editModel()
		if got != tt.want {
			t.Errorf("editModel(model=%q, editModel=%q) = %q, want %q", tt.model, tt.editModel, got, tt.want)
		}
	}
}

func TestVeniceProviderCapabilities(t *testing.T) {
	provider := NewVeniceProvider("key", "", "", "")
	if !provider.SupportsEdit() {
		t.Error("expected SupportsEdit() = true")
	}
	if !provider.SupportsMultiImage() {
		t.Error("expected SupportsMultiImage() = true")
	}
}
