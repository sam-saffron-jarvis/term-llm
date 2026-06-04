package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// GitHubBillingAPIVersion is the REST API version that introduced the current
	// Copilot AI credit billing usage endpoints.
	GitHubBillingAPIVersion = "2026-03-10"

	defaultGitHubAPIBaseURL = "https://api.github.com"

	// SourceGitHubBillingAPI identifies reports loaded from GitHub's official
	// billing usage API, not from Copilot's legacy/internal entitlement endpoint.
	SourceGitHubBillingAPI = "github_billing_api"
)

// ErrNoGitHubToken is returned when Copilot AI credit usage is requested without
// a GitHub REST token. Copilot's chat OAuth token is intentionally not used for
// billing reports because the official endpoints have their own permissions.
var ErrNoGitHubToken = errors.New("missing GitHub token for Copilot AI credit usage")

// Scope is the billing entity level to query.
type Scope string

const (
	ScopeUser         Scope = "user"
	ScopeOrganization Scope = "org"
	ScopeEnterprise   Scope = "enterprise"
)

// UsageFilters mirrors the query parameters supported by the latest GitHub
// Copilot AI credit usage endpoints. Callers should leave scope-unsupported
// filters empty; the client serializes only non-zero/non-empty filters.
type UsageFilters struct {
	Year         int
	Month        int
	Day          int
	User         string
	Organization string
	Model        string
	Product      string
	CostCenterID string
}

// TimePeriod identifies the period GitHub summarized in the report.
type TimePeriod struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

// UsageItem is one row from GitHub's AI credit usage report.
type UsageItem struct {
	Product           string  `json:"product,omitempty"`
	SKU               string  `json:"sku,omitempty"`
	Model             string  `json:"model,omitempty"`
	UnitType          string  `json:"unitType,omitempty"`
	PricePerUnit      float64 `json:"pricePerUnit,omitempty"`
	GrossQuantity     float64 `json:"grossQuantity,omitempty"`
	GrossAmountUSD    float64 `json:"grossAmount,omitempty"`
	DiscountQuantity  float64 `json:"discountQuantity,omitempty"`
	DiscountAmountUSD float64 `json:"discountAmount,omitempty"`
	NetQuantity       float64 `json:"netQuantity,omitempty"`
	NetAmountUSD      float64 `json:"netAmount,omitempty"`
}

// UsageTotals aggregates all report items.
type UsageTotals struct {
	GrossCredits      float64 `json:"grossCredits"`
	NetCredits        float64 `json:"netCredits"`
	GrossAmountUSD    float64 `json:"grossAmountUSD"`
	NetAmountUSD      float64 `json:"netAmountUSD"`
	DiscountAmountUSD float64 `json:"discountAmountUSD"`
}

// UsageReport is the normalized latest-only Copilot usage report used by the CLI.
type UsageReport struct {
	Scope      Scope       `json:"scope"`
	Entity     string      `json:"entity"`
	TimePeriod TimePeriod  `json:"timePeriod"`
	Items      []UsageItem `json:"items"`
	Totals     UsageTotals `json:"totals"`
	Source     string      `json:"source"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// UsageClient fetches Copilot AI credit usage from GitHub's official billing API.
type UsageClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// UsageClientOption customizes a UsageClient.
type UsageClientOption func(*UsageClient)

// WithBaseURL overrides the GitHub API base URL. It is mainly used by tests and
// can also support GitHub Enterprise Cloud dedicated subdomains.
func WithBaseURL(baseURL string) UsageClientOption {
	return func(c *UsageClient) {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(httpClient *http.Client) UsageClientOption {
	return func(c *UsageClient) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// NewUsageClient creates a latest Copilot AI credit usage client.
func NewUsageClient(token string, opts ...UsageClientOption) (*UsageClient, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNoGitHubToken
	}
	c := &UsageClient{
		token:   token,
		baseURL: defaultGitHubAPIBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.baseURL == "" {
		c.baseURL = defaultGitHubAPIBaseURL
	}
	return c, nil
}

// NewUsageClientFromEnv creates a usage client using GITHUB_TOKEN or GH_TOKEN.
// GITHUB_API_URL may be set to target GitHub Enterprise Cloud dedicated
// subdomains; explicit options override the environment base URL.
func NewUsageClientFromEnv(opts ...UsageClientOption) (*UsageClient, error) {
	token, _ := TokenFromEnv()
	if baseURL := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); baseURL != "" {
		opts = append([]UsageClientOption{WithBaseURL(baseURL)}, opts...)
	}
	return NewUsageClient(token, opts...)
}

// TokenFromEnv returns the first supported GitHub REST token and the environment
// variable it came from. GITHUB_TOKEN wins over GH_TOKEN for predictability.
func TokenFromEnv() (token string, envName string) {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token, "GITHUB_TOKEN"
	}
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token, "GH_TOKEN"
	}
	return "", ""
}

// BillingTokenStatus returns a human-readable status for auth diagnostics.
func BillingTokenStatus() string {
	_, envName := TokenFromEnv()
	if envName == "" {
		return "usage missing GITHUB_TOKEN/GH_TOKEN"
	}
	return "usage via " + envName
}

// GetAICreditUsage returns the latest Copilot AI credit usage report for the
// requested billing scope. If scope is user and entity is empty, the authenticated
// user's login is discovered via GET /user.
func (c *UsageClient) GetAICreditUsage(ctx context.Context, scope Scope, entity string, filters UsageFilters) (*UsageReport, error) {
	if scope == "" {
		scope = ScopeUser
	}
	entity = strings.TrimSpace(entity)

	if scope == ScopeUser && entity == "" {
		login, err := c.CurrentUsername(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover GitHub username for Copilot usage: %w", err)
		}
		entity = login
	}
	if entity == "" {
		return nil, fmt.Errorf("copilot %s usage requires --copilot-entity", scope)
	}

	endpoint, err := c.aiCreditUsageEndpoint(scope, entity)
	if err != nil {
		return nil, err
	}
	endpoint = addUsageFilters(endpoint, filters)

	var response usageAPIResponse
	if err := c.getJSON(ctx, endpoint, &response, scope); err != nil {
		return nil, err
	}

	reportEntity := response.entityForScope(scope)
	if reportEntity == "" {
		reportEntity = entity
	}
	report := &UsageReport{
		Scope:      scope,
		Entity:     reportEntity,
		TimePeriod: response.TimePeriod,
		Items:      response.UsageItems,
		Source:     SourceGitHubBillingAPI,
	}
	report.Totals = CalculateTotals(report.Items)
	return report, nil
}

// CurrentUsername returns the login for the authenticated GitHub token.
func (c *UsageClient) CurrentUsername(ctx context.Context) (string, error) {
	var response struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, c.baseURL+"/user", &response, ScopeUser); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Login) == "" {
		return "", fmt.Errorf("GitHub /user response did not include login")
	}
	return response.Login, nil
}

// CalculateTotals sums usage report items.
func CalculateTotals(items []UsageItem) UsageTotals {
	var totals UsageTotals
	for _, item := range items {
		totals.GrossCredits += item.GrossQuantity
		totals.NetCredits += item.NetQuantity
		totals.GrossAmountUSD += item.GrossAmountUSD
		totals.NetAmountUSD += item.NetAmountUSD
		totals.DiscountAmountUSD += item.DiscountAmountUSD
	}
	return totals
}

func (c *UsageClient) aiCreditUsageEndpoint(scope Scope, entity string) (string, error) {
	escaped := url.PathEscape(entity)
	switch scope {
	case ScopeUser:
		return c.baseURL + "/users/" + escaped + "/settings/billing/ai_credit/usage", nil
	case ScopeOrganization:
		return c.baseURL + "/organizations/" + escaped + "/settings/billing/ai_credit/usage", nil
	case ScopeEnterprise:
		return c.baseURL + "/enterprises/" + escaped + "/settings/billing/ai_credit/usage", nil
	default:
		return "", fmt.Errorf("unknown Copilot usage scope %q (use user, org, or enterprise)", scope)
	}
}

func addUsageFilters(endpoint string, filters UsageFilters) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := u.Query()
	if filters.Year > 0 {
		q.Set("year", strconv.Itoa(filters.Year))
	}
	if filters.Month > 0 {
		q.Set("month", strconv.Itoa(filters.Month))
	}
	if filters.Day > 0 {
		q.Set("day", strconv.Itoa(filters.Day))
	}
	if filters.User != "" {
		q.Set("user", filters.User)
	}
	if filters.Organization != "" {
		q.Set("organization", filters.Organization)
	}
	if filters.Model != "" {
		q.Set("model", filters.Model)
	}
	if filters.Product != "" {
		q.Set("product", filters.Product)
	}
	if filters.CostCenterID != "" {
		q.Set("cost_center_id", filters.CostCenterID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *UsageClient) getJSON(ctx context.Context, endpoint string, dest any, scope Scope) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create GitHub billing request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "term-llm")
	req.Header.Set("X-GitHub-Api-Version", GitHubBillingAPIVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub billing request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read GitHub billing response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &BillingAPIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body)), Scope: scope}
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode GitHub billing response: %w", err)
	}
	return nil
}

type usageAPIResponse struct {
	TimePeriod   TimePeriod  `json:"timePeriod"`
	User         string      `json:"user"`
	Organization string      `json:"organization"`
	Enterprise   string      `json:"enterprise"`
	UsageItems   []UsageItem `json:"usageItems"`
}

func (r usageAPIResponse) entityForScope(scope Scope) string {
	switch scope {
	case ScopeUser:
		return r.User
	case ScopeOrganization:
		return r.Organization
	case ScopeEnterprise:
		return r.Enterprise
	default:
		return ""
	}
}

// BillingAPIError describes a non-success response from GitHub's billing API.
type BillingAPIError struct {
	StatusCode int
	Body       string
	Scope      Scope
}

func (e *BillingAPIError) Error() string {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("GitHub Copilot AI Credit usage requires GitHub billing permissions (status %d). "+
			"Set GITHUB_TOKEN or GH_TOKEN with the required access. Personal usage requires user Plan: read; "+
			"organization usage requires organization Administration: read; enterprise usage requires enterprise admin or billing manager access. Response: %s", e.StatusCode, e.Body)
	case http.StatusNotFound:
		return fmt.Sprintf("GitHub Copilot AI Credit usage endpoint or entity not found for scope %q (status 404). "+
			"Check --copilot-scope and --copilot-entity. Response: %s", e.Scope, e.Body)
	default:
		return fmt.Sprintf("GitHub Copilot AI Credit usage API error (status %d): %s", e.StatusCode, e.Body)
	}
}
