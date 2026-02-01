package gist

import (
	"testing"
)

func TestParseGistRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "raw gist ID",
			input: "abc123def456",
			want:  "abc123def456",
		},
		{
			name:  "full URL with https",
			input: "https://gist.github.com/user/abc123def456",
			want:  "abc123def456",
		},
		{
			name:  "URL without scheme",
			input: "gist.github.com/user/abc123def456",
			want:  "abc123def456",
		},
		{
			name:  "URL with http",
			input: "http://gist.github.com/user/abc123def456",
			want:  "abc123def456",
		},
		{
			name:  "URL with trailing whitespace",
			input: "  https://gist.github.com/user/abc123def456  ",
			want:  "abc123def456",
		},
		{
			name:  "long gist ID",
			input: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
			want:  "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
		},
		{
			name:    "invalid - contains uppercase",
			input:   "ABC123",
			wantErr: true,
		},
		{
			name:    "invalid - random string",
			input:   "not-a-gist",
			wantErr: true,
		},
		{
			name:    "invalid - wrong domain",
			input:   "https://github.com/user/repo",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGistRef(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGistRef() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseGistRef() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetURL(t *testing.T) {
	got := GetURL("abc123def456")
	want := "https://gist.github.com/abc123def456"
	if got != want {
		t.Errorf("GetURL() = %v, want %v", got, want)
	}
}
