package image

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewGeminiProviderDefaults(t *testing.T) {
	p := NewGeminiProvider("key", "", "")
	if p.model != geminiDefaultModel {
		t.Errorf("expected default model %q, got %q", geminiDefaultModel, p.model)
	}
	if p.defaultSize != "" {
		t.Errorf("expected empty defaultSize, got %q", p.defaultSize)
	}
}

func TestNewGeminiProviderCustom(t *testing.T) {
	p := NewGeminiProvider("key", "gemini-2.0-flash", "4K")
	if p.model != "gemini-2.0-flash" {
		t.Errorf("expected model %q, got %q", "gemini-2.0-flash", p.model)
	}
	if p.defaultSize != "4K" {
		t.Errorf("expected defaultSize %q, got %q", "4K", p.defaultSize)
	}
}

func TestGeminiImageConfigSerialization(t *testing.T) {
	tests := []struct {
		name       string
		reqSize    string
		defaultSz  string
		wantConfig bool
		wantSize   string
	}{
		{"request size wins", "4K", "2K", true, "4K"},
		{"config default used", "", "2K", true, "2K"},
		{"both empty omits imageConfig", "", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			genCfg := geminiGenerationConfig{
				ResponseModalities: []string{"TEXT", "IMAGE"},
			}
			effectiveSize := tt.reqSize
			if effectiveSize == "" {
				effectiveSize = tt.defaultSz
			}
			if effectiveSize != "" {
				genCfg.ImageConfig = &geminiImageConfig{ImageSize: effectiveSize}
			}

			data, err := json.Marshal(genCfg)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			_, hasConfig := m["imageConfig"]
			if hasConfig != tt.wantConfig {
				t.Errorf("imageConfig present=%v, want %v (json: %s)", hasConfig, tt.wantConfig, data)
			}
			if tt.wantConfig {
				cfg := m["imageConfig"].(map[string]interface{})
				if got := cfg["imageSize"].(string); got != tt.wantSize {
					t.Errorf("imageSize=%q, want %q", got, tt.wantSize)
				}
			}
		})
	}
}

func TestTruncateBase64InJSON(t *testing.T) {
	longB64 := strings.Repeat("A", 5000)

	tests := []struct {
		name    string
		input   interface{}
		wantMax int // max length of output (sanity check)
	}{
		{
			"gemini style data field",
			map[string]interface{}{
				"candidates": []interface{}{
					map[string]interface{}{
						"content": map[string]interface{}{
							"parts": []interface{}{
								map[string]interface{}{
									"inlineData": map[string]interface{}{
										"mimeType": "image/png",
										"data":     longB64,
									},
								},
							},
						},
					},
				},
			},
			500,
		},
		{
			"venice style images array",
			map[string]interface{}{
				"images": []interface{}{longB64},
			},
			300,
		},
		{
			"openai style b64_json",
			map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"b64_json": longB64,
					},
				},
			},
			300,
		},
		{
			"short strings preserved",
			map[string]interface{}{
				"status": "ok",
				"data":   "short",
			},
			200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			result := truncateBase64InJSON(raw)

			if len(result) > tt.wantMax {
				t.Errorf("output too long: got %d chars, want <= %d\noutput: %s", len(result), tt.wantMax, result[:200])
			}
			if strings.Contains(result, longB64) {
				t.Error("output contains full untruncated base64")
			}
			if !strings.Contains(result, "truncated") && len(raw) > 200 {
				t.Error("expected truncation marker in output")
			}
		})
	}
}
