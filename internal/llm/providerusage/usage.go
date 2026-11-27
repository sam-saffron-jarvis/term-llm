package providerusage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/term-llm/term-llm/internal/proxyclient"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/credentials"
	"github.com/term-llm/term-llm/internal/grokprotocol"
	rp "github.com/term-llm/term-llm/internal/llm/providers/responsesproviders"
	"github.com/term-llm/term-llm/internal/providerhttp"
)

const (
	chatGPTUsageURL            = "https://chatgpt.com/backend-api/wham/usage"
	openCodeGoUsageURL         = rp.OpenCodeGoBaseURL + "/usage"
	grokUsageURL               = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	maxGrokUsageResponseBytes  = 4 << 20
	maxGrokUsageErrorBodyBytes = 4096
)

// Report is a live usage snapshot reported by a provider account.
type Report struct {
	Provider   string   `json:"provider"`
	Plan       string   `json:"plan,omitempty"`
	Source     string   `json:"source,omitempty"`
	Limits     []Limit  `json:"limits,omitempty"`
	Credits    *Credits `json:"credits,omitempty"`
	LimitState string   `json:"limit_state,omitempty"`
}

// Limit describes one provider-enforced usage limit.
type Limit struct {
	ID              string  `json:"id"`
	Name            string  `json:"name,omitempty"`
	Allowed         bool    `json:"allowed"`
	LimitReached    bool    `json:"limit_reached"`
	PrimaryWindow   *Window `json:"primary_window,omitempty"`
	SecondaryWindow *Window `json:"secondary_window,omitempty"`
}

// Window describes one provider usage window.
type Window struct {
	Label           string    `json:"label,omitempty"`
	UsedPercent     float64   `json:"used_percent"`
	PercentUnknown  bool      `json:"percent_unknown,omitempty"`
	DurationMinutes int       `json:"duration_minutes,omitempty"`
	ResetsAt        time.Time `json:"resets_at,omitempty"`
	Detail          string    `json:"detail,omitempty"`
}

// MarshalJSON represents an unmeasured percentage as null, not a measured zero.
func (w Window) MarshalJSON() ([]byte, error) {
	type fields Window
	var percent *float64
	if !w.PercentUnknown {
		percent = &w.UsedPercent
	}
	return json.Marshal(struct {
		fields
		UsedPercent *float64 `json:"used_percent"`
	}{fields(w), percent})
}

// Credits describes optional account credits returned by the provider.
type Credits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
	Currency   string `json:"currency,omitempty"`
}

type chatGPTUsageResponse struct {
	PlanType             string                        `json:"plan_type"`
	RateLimit            *chatGPTUsageRateLimit        `json:"rate_limit"`
	AdditionalRateLimits []chatGPTAdditionalUsageLimit `json:"additional_rate_limits"`
	Credits              *Credits                      `json:"credits"`
	RateLimitReachedType *struct {
		Type string `json:"type"`
	} `json:"rate_limit_reached_type"`
}

type chatGPTAdditionalUsageLimit struct {
	LimitName      string                 `json:"limit_name"`
	MeteredFeature string                 `json:"metered_feature"`
	RateLimit      *chatGPTUsageRateLimit `json:"rate_limit"`
}

type chatGPTUsageRateLimit struct {
	Allowed         *bool               `json:"allowed"`
	LimitReached    bool                `json:"limit_reached"`
	PrimaryWindow   *chatGPTUsageWindow `json:"primary_window"`
	SecondaryWindow *chatGPTUsageWindow `json:"secondary_window"`
}

type chatGPTUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

type openCodeGoUsageResponse struct {
	Usage struct {
		Rolling openCodeGoUsageWindow `json:"rolling"`
		Weekly  openCodeGoUsageWindow `json:"weekly"`
		Monthly openCodeGoUsageWindow `json:"monthly"`
	} `json:"usage"`
}

type openCodeGoUsageWindow struct {
	Status   string   `json:"status"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resetsAt"`
}

type grokUsageResponse struct {
	Config *grokUsageConfig `json:"config"`
}

type grokUsageConfig struct {
	CreditUsagePercent *float64         `json:"creditUsagePercent"`
	CurrentPeriod      *grokUsagePeriod `json:"currentPeriod"`
	MonthlyLimit       *grokUsageCent   `json:"monthlyLimit"`
	Used               *grokUsageCent   `json:"used"`
	OnDemandCap        *grokUsageCent   `json:"onDemandCap"`
	OnDemandUsed       *grokUsageCent   `json:"onDemandUsed"`
	PrepaidBalance     *grokUsageCent   `json:"prepaidBalance"`
	BillingPeriodStart string           `json:"billingPeriodStart"`
	BillingPeriodEnd   string           `json:"billingPeriodEnd"`
}

type grokUsagePeriod struct {
	Type  string `json:"type"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type grokUsageCent struct {
	Val int64 `json:"val"`
}

var httpClient = rp.ChatGPTHTTPClient()

// Fetch fetches current account-level usage directly from provider.
func Fetch(ctx context.Context, provider string) (*Report, error) {
	return FetchWithAPIKey(ctx, provider, "")
}

// FetchWithAPIKey fetches live usage with an explicitly resolved
// API key when the provider requires one. OAuth providers ignore apiKey.
func FetchWithAPIKey(ctx context.Context, provider, apiKey string) (*Report, error) {
	return FetchWithBaseURL(ctx, provider, apiKey, "")
}

// FetchWithBaseURL uses the inference API root for Grok and OpenCode Go usage.
// ChatGPT usage is a separate wham account service, not the Codex API.
func FetchWithBaseURL(ctx context.Context, provider, apiKey, baseURL string) (*Report, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "chatgpt":
		return fetchChatGPTProviderUsageWithRoot(ctx, baseURL)
	case "grok":
		return fetchGrokProviderUsage(ctx, baseURL)
	case "opencode-go":
		if strings.TrimSpace(apiKey) == "" {
			apiKey = os.Getenv("OPENCODE_API_KEY")
		}
		return fetchOpenCodeGoProviderUsage(ctx, apiKey, baseURL)
	default:
		return nil, fmt.Errorf("live usage is not supported for provider %q", provider)
	}
}

func fetchChatGPTProviderUsage(ctx context.Context) (*Report, error) {
	return fetchChatGPTProviderUsageWithRoot(ctx, "")
}

func fetchChatGPTProviderUsageWithRoot(ctx context.Context, apiRoot string) (*Report, error) {
	var base string
	if b, ok := proxyclient.Lookup(apiRoot); ok && b.Kind == "chatgpt" {
		// The sibling belongs to the selected provider, not necessarily the
		// route named chatgpt-backend or a neighboring URL path.
		sibling, ok := proxyclient.Related(apiRoot, "chatgpt-backend")
		if !ok {
			return nil, fmt.Errorf("ChatGPT account usage needs an explicit chatgpt-backend proxy grant")
		}
		base = sibling.Root
	} else {
		var err error
		base, err = providerhttp.EnvBaseURL("CHATGPT_USAGE_BASE_URL", "https://chatgpt.com/backend-api")
		if err != nil {
			return nil, err
		}
	}
	creds, err := credentials.GetChatGPTCredentialsForEndpoint(base)
	if err != nil {
		return nil, fmt.Errorf("load ChatGPT credentials: %w (run 'term-llm auth login chatgpt')", err)
	}
	if creds.IsExpired() {
		if err := credentials.RefreshChatGPTCredentials(creds); err != nil {
			return nil, fmt.Errorf("refresh ChatGPT credentials: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/wham/usage", nil)
	if err != nil {
		return nil, fmt.Errorf("create ChatGPT usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	if creds.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", creds.AccountID)
	}
	req.Header.Set("originator", rp.ChatGPTOriginator)
	req.Header.Set("User-Agent", rp.ChatGPTUserAgent())

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := proxyclient.Do(client, req)
	if err != nil {
		return nil, fmt.Errorf("fetch ChatGPT usage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := fmt.Sprintf("ChatGPT usage request failed: %s", resp.Status)
		if len(body) > 0 {
			message += ": " + string(body)
		}
		return nil, providerhttp.NewStatusErrorMessage(message, resp, body)
	}

	var decoded chatGPTUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ChatGPT usage: %w", err)
	}
	return normalizeChatGPTProviderUsage(decoded), nil
}

func fetchGrokProviderUsage(ctx context.Context, baseURL string) (*Report, error) {
	creds, err := credentials.GetGrokCredentialsForEndpoint(baseURL)
	if err != nil {
		return nil, fmt.Errorf("load Grok credentials: %w (run 'term-llm auth login grok')", err)
	}
	if creds.IsExpired() {
		if err := rp.RefreshGrokSession(ctx, creds, false); err != nil {
			return nil, fmt.Errorf("refresh Grok credentials: %w", err)
		}
	}

	resp, err := requestGrokProviderUsage(ctx, creds, baseURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return decodeGrokProviderUsageResponse(resp)
	}
	closeGrokUsageResponse(resp)
	if err := rp.RefreshGrokSession(ctx, creds, true); err != nil {
		return nil, fmt.Errorf("refresh rejected Grok credentials: %w", err)
	}

	resp, err = requestGrokProviderUsage(ctx, creds, baseURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		closeGrokUsageResponse(resp)
		return nil, fmt.Errorf("Grok usage request remained unauthorized after refreshing credentials; run 'term-llm auth login grok'")
	}
	return decodeGrokProviderUsageResponse(resp)
}

func closeGrokUsageResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxGrokUsageErrorBodyBytes))
	_ = resp.Body.Close()
}

func requestGrokProviderUsage(ctx context.Context, creds *credentials.GrokCredentials, baseURL string) (*http.Response, error) {
	endpoint := grokUsageURL
	if baseURL != "" {
		endpoint = baseURL + "/billing?format=credits"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Grok usage request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	// Grok's billing endpoint uses x-userid, unlike the Responses endpoint's
	// x-grok-user-id. This matches the official Grok Build x.ai/billing handler.
	req.Header.Set("x-userid", creds.AccountID)
	req.Header.Set("x-grok-client-version", rp.GrokProxyCompatibilityVersion)
	req.Header.Set("x-grok-client-mode", grokprotocol.ClientModeHeadless)
	req.Header.Set("User-Agent", rp.GrokUserAgent)

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := proxyclient.Do(client, req)
	if err != nil {
		return nil, fmt.Errorf("fetch Grok usage: %w", err)
	}
	return resp, nil
}

func decodeGrokProviderUsageResponse(resp *http.Response) (*Report, error) {
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxGrokUsageErrorBodyBytes))
		message := fmt.Sprintf("Grok usage request failed: %s", resp.Status)
		if len(body) > 0 {
			message += ": " + string(body)
		}
		return nil, providerhttp.NewStatusErrorMessage(message, resp, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGrokUsageResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Grok usage: %w", err)
	}
	if len(body) > maxGrokUsageResponseBytes {
		return nil, fmt.Errorf("decode Grok usage: response exceeded %d byte limit", maxGrokUsageResponseBytes)
	}
	var decoded grokUsageResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode Grok usage: %w", err)
	}
	return normalizeGrokProviderUsage(decoded)
}

func normalizeGrokProviderUsage(raw grokUsageResponse) (*Report, error) {
	if raw.Config == nil {
		return nil, fmt.Errorf("decode Grok usage: response did not contain billing config")
	}
	config := raw.Config
	// creditUsagePercent is a proto scalar and is omitted when its value is zero.
	// A config with a current period but no percentage therefore means 0% used,
	// as observed from the live endpoint and handled by the official harness.
	usedPercent := 0.0
	if config.CreditUsagePercent != nil {
		usedPercent = *config.CreditUsagePercent
	} else if config.MonthlyLimit != nil && config.MonthlyLimit.Val > 0 && config.Used != nil {
		usedPercent = float64(config.Used.Val) / float64(config.MonthlyLimit.Val) * 100
	}

	periodType := ""
	startText := strings.TrimSpace(config.BillingPeriodStart)
	endText := strings.TrimSpace(config.BillingPeriodEnd)
	if config.CurrentPeriod != nil {
		periodType = strings.ToUpper(strings.TrimSpace(config.CurrentPeriod.Type))
		// Current and legacy timestamps are alternate response generations. Never
		// combine period metadata or boundaries across those generations.
		startText = strings.TrimSpace(config.CurrentPeriod.Start)
		endText = strings.TrimSpace(config.CurrentPeriod.End)
	} else if config.MonthlyLimit != nil || startText != "" || endText != "" {
		periodType = "USAGE_PERIOD_TYPE_MONTHLY"
	}
	start, err := parseOptionalGrokUsageTime("period start", startText)
	if err != nil {
		return nil, err
	}
	reset, err := parseOptionalGrokUsageTime("period end", endText)
	if err != nil {
		return nil, err
	}

	name, label, duration := grokUsageWindowMetadata(periodType)
	if !start.IsZero() && reset.After(start) {
		duration = int(reset.Sub(start).Round(time.Minute) / time.Minute)
	}
	window := &Window{
		Label:           label,
		UsedPercent:     usedPercent,
		DurationMinutes: duration,
		ResetsAt:        reset,
	}
	onDemandHasHeadroom := false
	if config.OnDemandCap != nil {
		capCents := absoluteGrokUsageCents(config.OnDemandCap.Val)
		if capCents > 0 {
			usedCents := int64(0)
			if config.OnDemandUsed != nil {
				usedCents = absoluteGrokUsageCents(config.OnDemandUsed.Val)
			}
			onDemandHasHeadroom = usedCents < capCents
			// These display-facing money fields use accounting signs in Grok's
			// billing payload. The legacy included limit/used counters above do not.
			window.Detail = fmt.Sprintf("%s of %s pay-as-you-go used", formatGrokUsageDollars(usedCents), formatGrokUsageDollars(capCents))
		}
	}
	prepaidBalanceCents := int64(0)
	if config.PrepaidBalance != nil {
		prepaidBalanceCents = absoluteGrokUsageCents(config.PrepaidBalance.Val)
	}
	limitReached := usedPercent >= 100 && !onDemandHasHeadroom && prepaidBalanceCents == 0

	report := &Report{
		Provider: "grok",
		Limits: []Limit{{
			ID:            "included",
			Name:          name,
			Allowed:       !limitReached,
			LimitReached:  limitReached,
			PrimaryWindow: window,
		}},
	}
	if prepaidBalanceCents > 0 {
		report.Credits = &Credits{
			HasCredits: true,
			Balance:    formatGrokUsageDollarAmount(prepaidBalanceCents),
			Currency:   "USD",
		}
	}
	return report, nil
}

func parseOptionalGrokUsageTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode Grok usage %s %q: %w", field, value, err)
	}
	return parsed, nil
}

func grokUsageWindowMetadata(periodType string) (name, label string, duration int) {
	switch {
	case strings.Contains(periodType, "WEEKLY"):
		return "Weekly limit", "1 week window", 7 * 24 * 60
	case strings.Contains(periodType, "MONTHLY"):
		return "Monthly limit", "Monthly window", 0
	default:
		return "Included allowance", "", 0
	}
}

func absoluteGrokUsageCents(value int64) int64 {
	if value == -1<<63 {
		return 0
	}
	if value < 0 {
		return -value
	}
	return value
}

func formatGrokUsageDollarAmount(cents int64) string {
	cents = absoluteGrokUsageCents(cents)
	if cents%100 == 0 {
		return strconv.FormatInt(cents/100, 10)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func formatGrokUsageDollars(cents int64) string {
	return "$" + formatGrokUsageDollarAmount(cents)
}

func fetchOpenCodeGoProviderUsage(ctx context.Context, apiKey, baseURL string) (*Report, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("OpenCode Go usage requires an API key (configure providers.opencode-go.api_key or set OPENCODE_API_KEY)")
	}
	endpoint := openCodeGoUsageURL
	if baseURL != "" {
		endpoint = baseURL + "/usage"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create OpenCode Go usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rp.ApplyOpenCodeGoAttributionHeaders(req.Header, "")

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := proxyclient.Do(client, req)
	if err != nil {
		return nil, fmt.Errorf("fetch OpenCode Go usage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := fmt.Sprintf("OpenCode Go usage request failed: %s", resp.Status)
		if len(body) > 0 {
			message += ": " + string(body)
		}
		return nil, providerhttp.NewStatusErrorMessage(message, resp, body)
	}

	var decoded openCodeGoUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode OpenCode Go usage: %w", err)
	}
	return normalizeOpenCodeGoProviderUsage(decoded)
}

func normalizeOpenCodeGoProviderUsage(raw openCodeGoUsageResponse) (*Report, error) {
	report := &Report{Provider: "opencode-go"}
	windows := []struct {
		id       string
		name     string
		label    string
		duration int
		usage    openCodeGoUsageWindow
	}{
		{id: "rolling", name: "Rolling usage", label: "5 hour window", duration: 5 * 60, usage: raw.Usage.Rolling},
		{id: "weekly", name: "Weekly usage", label: "1 week window", duration: 7 * 24 * 60, usage: raw.Usage.Weekly},
		{id: "monthly", name: "Monthly usage", label: "Monthly window", usage: raw.Usage.Monthly},
	}
	for _, item := range windows {
		status := strings.TrimSpace(item.usage.Status)
		resetText := strings.TrimSpace(item.usage.ResetsAt)
		if item.usage.Percent == nil && resetText == "" && status == "" {
			continue
		}
		var reset time.Time
		if resetText != "" {
			var err error
			reset, err = time.Parse(time.RFC3339, resetText)
			if err != nil {
				return nil, fmt.Errorf("decode OpenCode Go %s reset time %q: %w", item.id, resetText, err)
			}
		}
		reached := strings.EqualFold(status, "rate-limited")
		window := usageWindow(item.usage.Percent)
		window.Label, window.DurationMinutes, window.ResetsAt = item.label, item.duration, reset
		report.Limits = append(report.Limits, Limit{
			ID:            item.id,
			Name:          item.name,
			Allowed:       !reached,
			LimitReached:  reached,
			PrimaryWindow: window,
		})
	}
	return report, nil
}

func normalizeChatGPTProviderUsage(raw chatGPTUsageResponse) *Report {
	report := &Report{Provider: "chatgpt", Plan: raw.PlanType, Credits: raw.Credits}
	if raw.RateLimitReachedType != nil {
		report.LimitState = raw.RateLimitReachedType.Type
	}
	if raw.RateLimit != nil {
		report.Limits = append(report.Limits, normalizeChatGPTUsageLimit("codex", "Codex", raw.RateLimit))
	}
	for _, additional := range raw.AdditionalRateLimits {
		if additional.RateLimit == nil {
			continue
		}
		id := strings.TrimSpace(additional.MeteredFeature)
		if id == "" {
			id = strings.TrimSpace(additional.LimitName)
		}
		report.Limits = append(report.Limits, normalizeChatGPTUsageLimit(id, additional.LimitName, additional.RateLimit))
	}
	return report
}

func normalizeChatGPTUsageLimit(id, name string, raw *chatGPTUsageRateLimit) Limit {
	reached := raw.LimitReached || (raw.Allowed != nil && !*raw.Allowed)
	return Limit{
		ID:              id,
		Name:            name,
		Allowed:         !reached,
		LimitReached:    reached,
		PrimaryWindow:   normalizeChatGPTUsageWindow(raw.PrimaryWindow, reached),
		SecondaryWindow: normalizeChatGPTUsageWindow(raw.SecondaryWindow, reached),
	}
}

func normalizeChatGPTUsageWindow(raw *chatGPTUsageWindow, reached bool) *Window {
	// A reached limit may apply to an unmeasured window, not its measured sibling.
	if raw == nil || (!reached && raw.UsedPercent == nil && raw.LimitWindowSeconds <= 0 && raw.ResetAt <= 0) {
		return nil
	}
	window := usageWindow(raw.UsedPercent)
	window.DurationMinutes = int(raw.LimitWindowSeconds / 60)
	if raw.ResetAt > 0 {
		window.ResetsAt = time.Unix(raw.ResetAt, 0)
	}
	return window
}

func usageWindow(percent *float64) *Window {
	if percent == nil {
		return &Window{PercentUnknown: true}
	}
	return &Window{UsedPercent: *percent}
}

// FormatOptions controls the shared CLI and TUI usage layout.
type FormatOptions struct {
	// Width is the available content width. Zero uses the default meter width.
	Width int
	// ASCII replaces block meter glyphs for limited terminals.
	ASCII bool
}

// Format formats a live provider usage report using default meter options.
func Format(report *Report, now time.Time) string {
	return FormatWithOptions(report, now, FormatOptions{})
}

// FormatWithOptions formats a responsive stacked provider usage report.
func FormatWithOptions(report *Report, now time.Time, opts FormatOptions) string {
	if report == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(providerUsageDisplayName(report.Provider))
	if metadata := formatProviderUsageMetadata(report); metadata != "" {
		b.WriteString("\n")
		b.WriteString(metadata)
	}
	for _, limit := range report.Limits {
		if limit.PrimaryWindow == nil && limit.SecondaryWindow == nil && !limit.LimitReached {
			continue
		}
		name := strings.TrimSpace(limit.Name)
		if name == "" {
			name = limit.ID
		}
		fmt.Fprintf(&b, "\n\n%s", name)
		if limit.PrimaryWindow != nil {
			formatProviderUsageWindow(&b, limit.PrimaryWindow, now, opts)
		}
		if limit.SecondaryWindow != nil {
			formatProviderUsageWindow(&b, limit.SecondaryWindow, now, opts)
		}
		if limit.LimitReached {
			b.WriteString("\n  ! Limit reached")
		}
	}
	if report.LimitState != "" {
		fmt.Fprintf(&b, "\n\n! %s", formatUsageLabel(report.LimitState))
	}
	return b.String()
}

func formatProviderUsageMetadata(report *Report) string {
	parts := make([]string, 0, 2)
	if plan := formatUsageLabel(report.Plan); plan != "" {
		parts = append(parts, plan+" plan")
	}
	if report.Credits != nil {
		switch {
		case report.Credits.Unlimited:
			parts = append(parts, "Unlimited credits")
		case positiveCreditBalance(report.Credits.Balance):
			balance := strings.TrimSpace(report.Credits.Balance)
			if strings.EqualFold(strings.TrimSpace(report.Credits.Currency), "USD") {
				balance = "$" + balance
			}
			parts = append(parts, balance+" credits")
		}
	}
	if report.Source != "" {
		parts = append(parts, report.Source)
	}
	return strings.Join(parts, " · ")
}

func positiveCreditBalance(balance string) bool {
	value, err := strconv.ParseFloat(strings.TrimSpace(balance), 64)
	return err == nil && value > 0
}

func formatProviderUsageWindow(b *strings.Builder, window *Window, now time.Time, opts FormatOptions) {
	label := strings.TrimSpace(window.Label)
	if label == "" {
		label = formatUsageWindowDuration(window.DurationMinutes)
	}
	fmt.Fprintf(b, "\n  %s", label)
	usedPercent := min(100, max(0, window.UsedPercent))
	elapsedFraction, projected, hasProjection := providerUsageProjection(window, now)
	barWidth := providerUsageBarWidth(opts)
	if window.PercentUnknown {
		b.WriteString("\n  Usage unknown")
	} else if barWidth > 0 {
		var marker *float64
		if hasProjection {
			marker = &elapsedFraction
		}
		fmt.Fprintf(b, "\n  %s  %.0f%% used", formatProviderUsageMeter(usedPercent, barWidth, opts.ASCII, marker), usedPercent)
	} else {
		fmt.Fprintf(b, "\n  %.0f%% used", usedPercent)
	}
	if window.Detail != "" {
		fmt.Fprintf(b, "\n  %s", window.Detail)
	}
	if !window.ResetsAt.IsZero() {
		fmt.Fprintf(b, "\n  Resets %s", formatUsageReset(window.ResetsAt, now))
		if hasProjection {
			if projected > 100 {
				b.WriteString(" · Projected: >100%")
			} else {
				fmt.Fprintf(b, " · Projected: ~%.0f%%", projected)
			}
		}
	}
	if hasProjection && projected > 100 {
		if opts.ASCII {
			b.WriteString("\n  ! Limit likely before reset at current pace")
		} else {
			b.WriteString("\n  ▲ Limit likely before reset at current pace")
		}
	}
}

func providerUsageProjection(window *Window, now time.Time) (elapsedFraction, projected float64, ok bool) {
	if window == nil || window.PercentUnknown {
		return 0, 0, false
	}
	usedPercent := min(100, max(0, window.UsedPercent))
	if window.DurationMinutes < 7*24*60 || window.ResetsAt.IsZero() || usedPercent < 2 {
		return 0, 0, false
	}
	duration := time.Duration(window.DurationMinutes) * time.Minute
	remaining := window.ResetsAt.Sub(now)
	if remaining <= 0 || remaining > duration {
		return 0, 0, false
	}
	elapsed := duration - remaining
	minimumElapsed := max(15*time.Minute, duration*15/100)
	if elapsed < minimumElapsed {
		return 0, 0, false
	}
	elapsedFraction = float64(elapsed) / float64(duration)
	if elapsedFraction <= 0 || elapsedFraction > 1 {
		return 0, 0, false
	}
	projected = usedPercent / elapsedFraction
	return elapsedFraction, projected, true
}

func providerUsageBarWidth(opts FormatOptions) int {
	const defaultWidth = 22
	if opts.Width <= 0 {
		return defaultWidth
	}
	overhead := 13 // indentation, spacing, and "100% used"
	if opts.ASCII {
		overhead += 2 // surrounding brackets
	}
	available := opts.Width - overhead
	if available < 8 {
		return 0
	}
	return min(defaultWidth, available)
}

func formatProviderUsageMeter(usedPercent float64, width int, ascii bool, elapsedFraction *float64) string {
	usedPercent = min(100, max(0, usedPercent))
	filled := 0
	if usedPercent > 0 {
		filled = int(math.Ceil(usedPercent * float64(width) / 100))
	}
	filled = min(width, max(0, filled))
	empty, full, marker := "░", "█", "│"
	if ascii {
		empty, full, marker = "-", "#", "|"
	}
	cells := make([]string, width)
	for i := range cells {
		cells[i] = empty
		if i < filled {
			cells[i] = full
		}
	}
	if elapsedFraction != nil && width > 0 {
		position := int(math.Round(min(1, max(0, *elapsedFraction)) * float64(width-1)))
		cells[position] = marker
	}
	meter := strings.Join(cells, "")
	if ascii {
		return "[" + meter + "]"
	}
	return meter
}

func formatUsageWindowDuration(minutes int) string {
	switch {
	case minutes <= 0:
		return "Window"
	case minutes%10080 == 0:
		return fmt.Sprintf("%d week window", minutes/10080)
	case minutes%1440 == 0:
		return fmt.Sprintf("%d day window", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%d hour window", minutes/60)
	default:
		return fmt.Sprintf("%d minute window", minutes)
	}
}

func formatUsageReset(reset, now time.Time) string {
	remaining := reset.Sub(now)
	if remaining > 0 {
		if duration := FormatDuration(remaining); duration != "" {
			return "in " + duration
		}
		return "in less than a minute"
	}
	return reset.Local().Format("Jan 2, 3:04 PM")
}

// FormatDuration renders d rounded to the minute as days, hours and minutes,
// e.g. "1d 4h 5m", omitting zero parts. It returns "" below one minute.
func FormatDuration(d time.Duration) string {
	totalMinutes := int64(d.Round(time.Minute) / time.Minute)
	if totalMinutes < 1 {
		return ""
	}
	days := totalMinutes / (24 * 60)
	hours := totalMinutes % (24 * 60) / 60
	minutes := totalMinutes % 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

func providerUsageDisplayName(provider string) string {
	switch {
	case strings.EqualFold(provider, "chatgpt"):
		return "ChatGPT Codex"
	case strings.EqualFold(provider, "opencode-go"):
		return "OpenCode Go"
	}
	return formatUsageLabel(provider)
}

func formatUsageLabel(value string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value)))
	for i := range words {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, " ")
}
