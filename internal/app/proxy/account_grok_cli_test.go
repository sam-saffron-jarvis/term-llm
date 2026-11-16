package proxy

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGrokAccountAllowsNativeAndCLIInferenceButNotAdministration(t *testing.T) {
	target, _ := url.Parse("https://cli-chat-proxy.grok.com/v1")
	route := activeRoute{Route: Route{Account: "grok", Upstream: target.String()}, target: target}
	account := accountRecord{AccountInfo: AccountInfo{Kind: "grok"}}
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/v1/responses", true},
		{"POST", "/v1/chat/completions", true},
		{"GET", "/v1/models", true},
		{"DELETE", "/v1/chat/completions", false},
		{"POST", "/v1/chat/completions-extra", false},
		{"POST", "/v1/api_keys", false},
		{"POST", "/v1/billing", false},
	} {
		req := httptest.NewRequest(tc.method, "https://cli-chat-proxy.grok.com"+tc.path, nil)
		if got := accountRequestAllowed(account, req, route); got != tc.want {
			t.Errorf("%s %s: %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
