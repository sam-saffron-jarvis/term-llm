package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/credentials"
)

func stubCopilotSessionRefresh(t *testing.T, apiBaseURL string) *int {
	t.Helper()

	origClient := copilotHTTPClient
	t.Cleanup(func() {
		copilotHTTPClient = origClient
	})

	requests := 0
	copilotHTTPClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if got := r.Header.Get("Authorization"); got != "token oauth-token" {
				t.Fatalf("Authorization header = %q, want GitHub OAuth token", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
					`{"token":"fresh-token","expires_at":%d,"refresh_in":1500,"endpoints":{"api":%q}}`,
					time.Now().Add(25*time.Minute).Unix(), apiBaseURL,
				))),
			}, nil
		}),
	}
	return &requests
}

func TestEnsureValidSessionKeepsUnexpiredToken(t *testing.T) {
	requests := stubCopilotSessionRefresh(t, "https://api.githubcopilot.com")

	provider := &CopilotProvider{
		creds:              &credentials.CopilotCredentials{AccessToken: "oauth-token"},
		sessionToken:       "existing-token",
		sessionTokenExpiry: time.Now().Add(10 * time.Minute),
	}

	if err := provider.ensureValidSession(context.Background()); err != nil {
		t.Fatalf("ensureValidSession returned error: %v", err)
	}
	if *requests != 0 {
		t.Fatalf("expected no token refresh request, got %d", *requests)
	}
	if provider.sessionToken != "existing-token" {
		t.Fatalf("expected existing session token to be kept, got %q", provider.sessionToken)
	}
}

func TestEnsureValidSessionRefreshesNearExpiry(t *testing.T) {
	requests := stubCopilotSessionRefresh(t, "https://api.business.githubcopilot.com")

	provider := &CopilotProvider{
		creds:              &credentials.CopilotCredentials{AccessToken: "oauth-token"},
		sessionToken:       "stale-token",
		sessionTokenExpiry: time.Now().Add(30 * time.Second),
	}

	if err := provider.ensureValidSession(context.Background()); err != nil {
		t.Fatalf("ensureValidSession returned error: %v", err)
	}
	if *requests != 1 {
		t.Fatalf("expected 1 token refresh request, got %d", *requests)
	}
	if provider.sessionToken != "fresh-token" {
		t.Fatalf("expected session token to be refreshed, got %q", provider.sessionToken)
	}
	if provider.apiBaseURL != "https://api.business.githubcopilot.com" {
		t.Fatalf("expected api base URL to be updated from token response, got %q", provider.apiBaseURL)
	}
}

func TestEnsureValidSessionRefreshesMissingExpiry(t *testing.T) {
	requests := stubCopilotSessionRefresh(t, "https://api.githubcopilot.com")

	provider := &CopilotProvider{
		creds:        &credentials.CopilotCredentials{AccessToken: "oauth-token"},
		sessionToken: "token-without-expiry",
	}

	if err := provider.ensureValidSession(context.Background()); err != nil {
		t.Fatalf("ensureValidSession returned error: %v", err)
	}
	if *requests != 1 {
		t.Fatalf("expected 1 token refresh request, got %d", *requests)
	}
	if provider.sessionToken != "fresh-token" {
		t.Fatalf("expected session token to be refreshed, got %q", provider.sessionToken)
	}
}

func TestCopilotListModelsFetchesLiveModelsAndCachesThem(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	origClient := copilotHTTPClient
	t.Cleanup(func() { copilotHTTPClient = origClient })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Fatalf("Authorization header = %q, want Bearer session-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"data": [
				{"id":"dynamic-copilot-model","name":"Dynamic Copilot Model","vendor":"github","capabilities":{"limits":{"max_prompt_tokens":123456,"max_context_window_tokens":200000,"max_output_tokens":64000}}},
				{"id":"dynamic-preview-model","name":"Dynamic Preview","vendor":"openai","preview":true},
				{"id":"gpt-5.5","name":"GPT 5.5","vendor":"openai","capabilities":{"limits":{"max_context_window_tokens":1050000,"max_output_tokens":128000}}}
			]
		}`)
	}))
	defer server.Close()
	copilotHTTPClient = server.Client()

	provider := &CopilotProvider{
		creds:              &credentials.CopilotCredentials{AccessToken: "oauth-token"},
		apiBaseURL:         server.URL,
		sessionToken:       "session-token",
		sessionTokenExpiry: time.Now().Add(time.Hour),
	}

	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models len = %d, want 3", len(models))
	}
	if models[0].ID != "dynamic-copilot-model" || models[0].DisplayName != "Dynamic Copilot Model" || models[0].OwnedBy != "github" {
		t.Fatalf("first model not parsed as expected: %+v", models[0])
	}
	if models[0].InputLimit != 123_456 {
		t.Fatalf("dynamic prompt token limit = %d, want 123456", models[0].InputLimit)
	}
	if models[1].DisplayName != "Dynamic Preview (preview)" {
		t.Fatalf("preview display name = %q, want Dynamic Preview (preview)", models[1].DisplayName)
	}
	if models[2].ID != "gpt-5.5" || models[2].InputLimit != 1_030_000 {
		t.Fatalf("gpt-5.5 list metadata = %+v, want 1,030,000 input limit", models[2])
	}

	ids := ProviderModelIDs("copilot")
	if !containsModelID(ids, "dynamic-copilot-model") || !containsModelID(ids, "dynamic-preview-model") || !containsModelID(ids, "gpt-5.5") {
		t.Fatalf("cached ProviderModelIDs(copilot) = %v, want fetched dynamic models", ids)
	}
	if got := InputLimitForProviderModel("copilot", "gpt-5.5"); got != 1_030_000 {
		t.Fatalf("cached copilot gpt-5.5 input limit = %d, want 1030000", got)
	}
}
