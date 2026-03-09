package clipboard

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestOSC52Sequence(t *testing.T) {
	text := "hello **world**"
	got := OSC52Sequence(text)

	// Verify format: ESC ] 52 ; c ; <base64> ESC backslash
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	want := "\033]52;c;" + payload + "\033\\"
	if got != want {
		t.Fatalf("OSC52Sequence() = %q, want %q", got, want)
	}

	// Verify payload round-trips
	// Extract base64 portion between "c;" and ESC
	prefix := "\033]52;c;"
	suffix := "\033\\"
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("unexpected framing in %q", got)
	}
	b64 := got[len(prefix) : len(got)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("invalid base64 in sequence: %v", err)
	}
	if string(decoded) != text {
		t.Fatalf("round-trip decoded = %q, want %q", string(decoded), text)
	}
}

func TestOSC52SequenceTmux(t *testing.T) {
	// Simulate tmux environment
	old := os.Getenv("TMUX")
	os.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	defer os.Setenv("TMUX", old)

	got := OSC52Sequence("hi")

	// Must be wrapped in DCS passthrough: ESC P tmux; ... ESC backslash
	if !strings.HasPrefix(got, "\033Ptmux;") {
		t.Fatalf("expected tmux DCS prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "\033\\") {
		t.Fatalf("expected ST suffix, got %q", got)
	}
	// Inner ESC chars should be doubled
	inner := got[len("\033Ptmux;") : len(got)-len("\033\\")]
	if !strings.Contains(inner, "\033\033]52;c;") {
		t.Fatalf("expected doubled ESC in inner sequence, got %q", inner)
	}
}

func TestOSC52SequenceScreen(t *testing.T) {
	// Simulate GNU screen environment
	oldTmux := os.Getenv("TMUX")
	oldSty := os.Getenv("STY")
	os.Setenv("TMUX", "")
	os.Setenv("STY", "12345.pts-0.hostname")
	defer func() {
		os.Setenv("TMUX", oldTmux)
		os.Setenv("STY", oldSty)
	}()

	got := OSC52Sequence("hi")
	if !strings.HasPrefix(got, "\033P") {
		t.Fatalf("expected screen DCS prefix, got %q", got)
	}
	if !strings.Contains(got, "\033]52;c;") {
		t.Fatalf("expected OSC 52 inside DCS, got %q", got)
	}
}

func TestDetectPreferredImageMIME(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "prefers png when available",
			in:   "text/plain\nimage/jpeg\nimage/png\n",
			want: "image/png",
		},
		{
			name: "accepts jpeg when png absent",
			in:   "text/plain\nimage/jpg\n",
			want: "image/jpeg",
		},
		{
			name: "accepts first image type as fallback",
			in:   "text/plain\nimage/bmp\napplication/json\n",
			want: "image/bmp",
		},
		{
			name: "ignores non image types",
			in:   "text/plain\napplication/octet-stream\n",
			want: "",
		},
		{
			name: "strips mime parameters",
			in:   "image/webp; charset=binary\n",
			want: "image/webp",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectPreferredImageMIME(tc.in)
			if got != tc.want {
				t.Fatalf("detectPreferredImageMIME() = %q, want %q", got, tc.want)
			}
		})
	}
}
