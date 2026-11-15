package providerhttp

import (
	"strings"
	"testing"
)

func TestEnvBaseURL(t *testing.T) {
	t.Setenv("AUDIT_HTTP_BASE", "")
	if got, err := EnvBaseURL("AUDIT_HTTP_BASE", "https://default.test/api/"); err != nil || got != "https://default.test/api" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	t.Setenv("AUDIT_HTTP_BASE", "https://proxy.test/nested/v1///")
	if got, err := EnvBaseURL("AUDIT_HTTP_BASE", "https://default.test"); err != nil || got != "https://proxy.test/nested/v1" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, raw := range []string{"https://user:secret@example.test", "https://example.test/?secret=1", "https://example.test/#secret", "file:///tmp/root"} {
		t.Setenv("AUDIT_HTTP_BASE", raw)
		if _, err := EnvBaseURL("AUDIT_HTTP_BASE", "https://default.test"); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("err=%v", err)
		}
	}
}
