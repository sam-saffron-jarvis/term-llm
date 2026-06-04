package copilot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageClientGetAICreditUsageUserDiscoversLoginAndTotals(t *testing.T) {
	var sawUsageRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != GitHubBillingAPIVersion {
			t.Fatalf("X-GitHub-Api-Version = %q, want %q", got, GitHubBillingAPIVersion)
		}

		switch r.URL.Path {
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"octocat"}`))
		case "/users/octocat/settings/billing/ai_credit/usage":
			sawUsageRequest = true
			if got := r.URL.Query().Get("year"); got != "2026" {
				t.Fatalf("year query = %q, want 2026", got)
			}
			if got := r.URL.Query().Get("month"); got != "6" {
				t.Fatalf("month query = %q, want 6", got)
			}
			if got := r.URL.Query().Get("model"); got != "Claude Sonnet 4.6" {
				t.Fatalf("model query = %q, want Claude Sonnet 4.6", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"timePeriod":{"year":2026,"month":6},
				"user":"octocat",
				"usageItems":[
					{"product":"Copilot","sku":"Copilot AI Credits","model":"Claude Sonnet 4.6","unitType":"credits","pricePerUnit":0.01,"grossQuantity":100,"grossAmount":1,"discountQuantity":10,"discountAmount":0.10,"netQuantity":90,"netAmount":0.90},
					{"product":"Copilot","sku":"Copilot AI Credits","model":"GPT-5 mini","unitType":"credits","pricePerUnit":0.01,"grossQuantity":50.5,"grossAmount":0.505,"discountQuantity":0,"discountAmount":0,"netQuantity":50.5,"netAmount":0.505}
				]
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewUsageClient("test-token", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewUsageClient: %v", err)
	}

	report, err := client.GetAICreditUsage(context.Background(), ScopeUser, "", UsageFilters{
		Year:  2026,
		Month: 6,
		Model: "Claude Sonnet 4.6",
	})
	if err != nil {
		t.Fatalf("GetAICreditUsage: %v", err)
	}
	if !sawUsageRequest {
		t.Fatal("usage endpoint was not requested")
	}
	if report.Scope != ScopeUser || report.Entity != "octocat" {
		t.Fatalf("scope/entity = %q/%q, want user/octocat", report.Scope, report.Entity)
	}
	if report.Source != SourceGitHubBillingAPI {
		t.Fatalf("source = %q, want %q", report.Source, SourceGitHubBillingAPI)
	}
	if report.TimePeriod.Year != 2026 || report.TimePeriod.Month != 6 {
		t.Fatalf("time period = %+v, want 2026/6", report.TimePeriod)
	}
	if got, want := report.Totals.GrossCredits, 150.5; got != want {
		t.Fatalf("GrossCredits = %v, want %v", got, want)
	}
	if got, want := report.Totals.NetCredits, 140.5; got != want {
		t.Fatalf("NetCredits = %v, want %v", got, want)
	}
	if got, want := report.Totals.NetAmountUSD, 1.405; got != want {
		t.Fatalf("NetAmountUSD = %v, want %v", got, want)
	}
}

func TestUsageClientRequiresEntityForOrgAndEnterprise(t *testing.T) {
	client, err := NewUsageClient("token")
	if err != nil {
		t.Fatalf("NewUsageClient: %v", err)
	}
	for _, scope := range []Scope{ScopeOrganization, ScopeEnterprise} {
		_, err := client.GetAICreditUsage(context.Background(), scope, "", UsageFilters{})
		if err == nil {
			t.Fatalf("scope %q with empty entity: expected error", scope)
		}
		if !strings.Contains(err.Error(), "entity") {
			t.Fatalf("scope %q error = %v, want mention entity", scope, err)
		}
	}
}

func TestNewUsageClientFromEnvRequiresGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	_, err := NewUsageClientFromEnv()
	if !errors.Is(err, ErrNoGitHubToken) {
		t.Fatalf("NewUsageClientFromEnv error = %v, want ErrNoGitHubToken", err)
	}
}

func TestUsageClientPermissionErrorIsActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by personal access token"}`, http.StatusForbidden)
	}))
	defer server.Close()

	client, err := NewUsageClient("token", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewUsageClient: %v", err)
	}
	_, err = client.GetAICreditUsage(context.Background(), ScopeUser, "octocat", UsageFilters{})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"billing permissions", "GITHUB_TOKEN", "Plan: read"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}
