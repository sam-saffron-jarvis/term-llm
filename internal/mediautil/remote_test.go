package mediautil

import (
	"strings"
	"testing"
)

func TestRemoteURLValidation(t *testing.T) {
	for _, url := range []string{"https://example.org/a.png?token=signed", "http://127.0.0.1:8080/a", "https://[::1]/image"} {
		if err := ValidateRemoteURL(url); err != nil {
			t.Errorf("valid URL rejected: %v", err)
		}
	}
	for _, url := range []string{"", "file:///tmp/x", "https://user:password@example.org/a", "https:///missing", "https://example.org/#fragment", "https://example.org/#", "https://example.org/\nsecret", "https://example.org/a b", "https://example.org/\\x", "https://example.org:bad/x", strings.Repeat("x", 8193)} {
		if err := ValidateRemoteURL(url); err == nil {
			t.Error("invalid URL accepted")
		}
	}
}

func TestDiagnosticReferenceRemovesSignedTokens(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"https://example.org/image.png?token=secret#fragment", "https://example.org/image.png"}, {"https://user:secret@example.org/image?sig=secret", "https://example.org/image"}, {"input.png", "input.png"}, {"http://%secret", "[remote reference]"}} {
		if got := DiagnosticReference(tc.in); got != tc.want {
			t.Errorf("diagnostic=%q want=%q", got, tc.want)
		}
	}
}
