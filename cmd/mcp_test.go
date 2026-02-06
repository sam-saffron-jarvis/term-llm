package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseValue(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"42", int64(42)},
		{"3.14", 3.14},
		{"hello", "hello"},
		{"", ""},
	}
	for _, tt := range tests {
		got := parseValue(tt.input)
		if got != tt.want {
			t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.input, got, got, tt.want, tt.want)
		}
	}
}

func TestReadFileArg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("file contents here"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := readFileArg(path)
	if err != nil {
		t.Fatalf("readFileArg(%q) error: %v", path, err)
	}
	if got != "file contents here" {
		t.Errorf("readFileArg(%q) = %q, want %q", path, got, "file contents here")
	}
}

func TestReadFileArgMissing(t *testing.T) {
	_, err := readFileArg("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestFormatSchemaParams(t *testing.T) {
	tests := []struct {
		name      string
		schema    map[string]any
		maxParams int
		want      string
	}{
		{
			name:      "empty",
			schema:    map[string]any{},
			maxParams: 5,
			want:      "",
		},
		{
			name: "with required",
			schema: map[string]any{
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
					"mode": map[string]any{"type": "string"},
				},
				"required": []any{"path"},
			},
			maxParams: 5,
			want:      "(mode, path*)",
		},
		{
			name: "truncated",
			schema: map[string]any{
				"properties": map[string]any{
					"a": map[string]any{},
					"b": map[string]any{},
					"c": map[string]any{},
				},
			},
			maxParams: 2,
			want:      "(a, b, ...)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSchemaParams(tt.schema, tt.maxParams)
			if got != tt.want {
				t.Errorf("formatSchemaParams() = %q, want %q", got, tt.want)
			}
		})
	}
}
