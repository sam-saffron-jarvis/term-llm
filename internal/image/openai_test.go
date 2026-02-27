package image

import "testing"

func TestOpenAISizeFromAspectRatio(t *testing.T) {
	tests := []struct {
		name  string
		ratio string
		want  string
	}{
		{"1:1", "1:1", "1024x1024"},
		{"empty defaults to square", "", "1024x1024"},
		{"16:9", "16:9", "1536x1024"},
		{"4:3", "4:3", "1536x1024"},
		{"3:2", "3:2", "1536x1024"},
		{"9:16", "9:16", "1024x1536"},
		{"3:4", "3:4", "1024x1536"},
		{"2:3", "2:3", "1024x1536"},
		{"unknown defaults to square", "unknown", "1024x1024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openaiSizeFromAspectRatio(tt.ratio)
			if got != tt.want {
				t.Errorf("openaiSizeFromAspectRatio(%q) = %q, want %q", tt.ratio, got, tt.want)
			}
		})
	}
}
