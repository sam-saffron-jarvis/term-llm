package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBaseURL(t *testing.T) {
	t.Setenv("AUDIT_BASE", "https://proxy.example/nested/api/v1///")
	file := filepath.Join(t.TempDir(), "endpoint")
	if err := os.WriteFile(file, []byte("https://file.example/proxy/v1/"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ raw, fallback, want string }{
		{"", "https://default.example/v1/", "https://default.example/v1"},
		{"${AUDIT_BASE}", "https://default.example", "https://proxy.example/nested/api/v1"},
		{"file://" + file, "", "https://file.example/proxy/v1"},
		{"${ABSENT_AUDIT_BASE}", "https://default.example", "https://default.example"},
		{"", "", ""},
	} {
		got, err := ResolveBaseURL("feature.base_url", tc.raw, tc.fallback)
		if err != nil || got != tc.want {
			t.Errorf("got=%q err=%v want=%q", got, err, tc.want)
		}
	}
	for _, raw := range []string{"ftp://example.test", "//example.test", "https://user:secret@example.test", "https://example.test/?token=secret", "https://example.test/#secret", "https://example.test/?", "https://example.test/#"} {
		_, err := ResolveBaseURL("feature.base_url", raw, "")
		if err == nil || !strings.Contains(err.Error(), "feature.base_url") || strings.Contains(err.Error(), "secret") {
			t.Errorf("error=%v", err)
		}
	}
}

func TestProviderBaseURLPrecedenceAndIsolation(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderConfig{"gemini": {BaseURL: "https://shared.example/prefix/", APIKey: "file:///not-used-by-endpoint", Headers: map[string]string{"unused": "file:///not-used"}}}}
	for _, tc := range []struct{ raw, want string }{{"https://feature.example/api/", "https://feature.example/api"}, {"", "https://shared.example/prefix"}} {
		got, err := cfg.ProviderBaseURL("gemini", tc.raw, "https://default.example")
		if err != nil || got != tc.want {
			t.Fatalf("got=%q err=%v", got, err)
		}
	}
	cfg.Providers["gemini"] = ProviderConfig{Type: ProviderTypeOpenAICompat, BaseURL: "https://different.example"}
	got, err := cfg.ProviderBaseURL("gemini", "", "https://default.example")
	if err != nil || got != "https://default.example" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	cfg.Providers["gemini"] = ProviderConfig{BaseURL: "file:///nonexistent-audit-base"}
	if _, err = cfg.ProviderBaseURL("gemini", "", "https://default.example"); err == nil {
		t.Fatal("explicit deferred failure fell back")
	}
}

func TestExplicitGrokSubscriptionBaseURL(t *testing.T) {
	if err := ValidateProviderCompatibility("grok", &ProviderConfig{Type: ProviderTypeGrok, BaseURL: "https://gateway.example/v1"}); err != nil {
		t.Fatal(err)
	}
}
