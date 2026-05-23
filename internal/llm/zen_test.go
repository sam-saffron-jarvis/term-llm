package llm

import "testing"

func TestNormalizeZenModel(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"bigpickle", "big-pickle"},
		{"BigPickle", "big-pickle"},
		{"big_pickle", "big-pickle"},
		{"big pickle", "big-pickle"},
		{" big-pickle ", "big-pickle"},
		{"minimax-m2.5-free", "minimax-m2.5-free"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeZenModel(tt.in); got != tt.want {
				t.Fatalf("normalizeZenModel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewZenProviderNormalizesBigPickleAlias(t *testing.T) {
	provider := NewZenProvider("", "bigpickle")
	if got := provider.model; got != "big-pickle" {
		t.Fatalf("NewZenProvider model = %q, want big-pickle", got)
	}
}
