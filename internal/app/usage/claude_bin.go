package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/term-llm/term-llm/internal/app/proxyrun"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/providerusage"
)

const claudeBinUsageRequestID = "term-llm-usage"

type claudeBinUsageEnvelope struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	} `json:"response"`
}

type claudeBinUsageReport struct {
	SubscriptionType    *string              `json:"subscription_type"`
	RateLimitsAvailable bool                 `json:"rate_limits_available"`
	RateLimits          *claudeBinRateLimits `json:"rate_limits"`
}

type claudeBinRateLimits struct {
	FiveHour          *claudeBinRateLimit   `json:"five_hour"`
	SevenDay          *claudeBinRateLimit   `json:"seven_day"`
	SevenDayOAuthApps *claudeBinRateLimit   `json:"seven_day_oauth_apps"`
	SevenDayOpus      *claudeBinRateLimit   `json:"seven_day_opus"`
	SevenDaySonnet    *claudeBinRateLimit   `json:"seven_day_sonnet"`
	ModelScoped       []claudeBinModelLimit `json:"model_scoped"`
	ExtraUsage        *claudeBinExtraUsage  `json:"extra_usage"`
}

type claudeBinRateLimit struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeBinModelLimit struct {
	DisplayName string   `json:"display_name"`
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeBinExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit"`
	UsedCredits  *float64 `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
	Currency     *string  `json:"currency"`
}

type claudeBinUsageRow struct {
	Label           string
	WindowLabel     string
	DurationMinutes int
	Utilization     *float64
	ResetsAt        *string
	Detail          string
}

func validateClaudeBinUsageFlags(cmd interface{ Flags() *pflag.FlagSet }) error {
	var incompatible []string
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name != "provider" && flag.Name != "json" {
			incompatible = append(incompatible, "--"+flag.Name)
		}
	})
	if len(incompatible) > 0 {
		sort.Strings(incompatible)
		return fmt.Errorf("%s cannot be used with --provider claude-bin; live subscription usage only supports --json", strings.Join(incompatible, ", "))
	}
	return nil
}

var (
	errClaudeBinUsageUnavailable = errors.New("Claude plan usage is unavailable: Claude Code needs claude.ai subscription OAuth with the user:profile scope; API-key and third-party-provider sessions do not expose plan limits")
	errClaudeBinNoUsageWindows   = errors.New("Claude returned no active usage windows")
)

func runClaudeBinUsage(parent context.Context, cmdOut io.Writer, jsonOutput bool) error {
	raw, account, err := loadClaudeBinUsage(parent)
	if err != nil {
		return err
	}
	if jsonOutput {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("format Claude Code usage response: %w", err)
		}
		_, err := fmt.Fprintln(cmdOut, pretty.String())
		return err
	}

	report, err := decodeClaudeBinUsage(raw)
	if err != nil {
		return err
	}
	return writeClaudeBinUsageWithSource(cmdOut, report, time.Now(), providerUsageFormatOptions(cmdOut), claudeBinUsageSource(account.proxyRoot))
}

// claudeBinUsageAccount says which account get_usage asked about: a
// credential-proxy root, or the direct login started with env.
type claudeBinUsageAccount struct {
	proxyRoot string
	env       []string
}

// loadClaudeBinUsage returns Claude Code's raw get_usage response and the account it describes.
func loadClaudeBinUsage(parent context.Context) (json.RawMessage, claudeBinUsageAccount, error) {
	connection, proxied, err := configuredClaudeUsageProxy()
	if err != nil {
		return nil, claudeBinUsageAccount{}, err
	}
	return loadClaudeBinUsageWithConnection(parent, connection, proxied)
}

func loadClaudeBinUsageWithConfig(parent context.Context, cfg *config.Config, providerName string) (json.RawMessage, claudeBinUsageAccount, error) {
	connection, proxied, err := configuredClaudeUsageProxyFor(cfg, providerName)
	if err != nil {
		return nil, claudeBinUsageAccount{}, err
	}
	return loadClaudeBinUsageWithConnection(parent, connection, proxied)
}

func loadClaudeBinUsageWithConnection(parent context.Context, connection proxyrun.Connection, proxied bool) (json.RawMessage, claudeBinUsageAccount, error) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return nil, claudeBinUsageAccount{}, errors.New("claude-bin usage requires the Claude Code CLI in PATH")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	if proxied {
		raw, err := fetchClaudeBinProxiedUsage(ctx, binary, connection)
		return raw, claudeBinUsageAccount{proxyRoot: connection.Root}, err
	}
	// ANTHROPIC_API_KEY would force API-key auth instead of the subscription login.
	env := withoutEnvironmentVariable(os.Environ(), "ANTHROPIC_API_KEY")
	raw, err := fetchClaudeBinUsage(ctx, binary, env)
	return raw, claudeBinUsageAccount{env: env}, err
}

func claudeBinUsageSource(proxyRoot string) string {
	if proxyRoot != "" {
		return "Live through credential proxy"
	}
	return "Live from Claude Code"
}

func decodeClaudeBinUsage(raw json.RawMessage) (claudeBinUsageReport, error) {
	var report claudeBinUsageReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return claudeBinUsageReport{}, fmt.Errorf("decode Claude Code usage response: %w", err)
	}
	return report, nil
}

// fetchClaudeBinUsage is the direct, unproxied native runner seam. It runs
// get_usage with env, the established environment minus ANTHROPIC_API_KEY.
var fetchClaudeBinUsage = func(ctx context.Context, binary string, env []string) (json.RawMessage, error) {
	cmd := newClaudeUsageCommand(ctx, binary)
	cmd.Env = env
	return runClaudeUsageCommand(ctx, cmd)
}

func newClaudeUsageCommand(ctx context.Context, binary string) *exec.Cmd {
	request := fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":"get_usage"}}`+"\n", claudeBinUsageRequestID)
	command := exec.CommandContext(ctx, binary,
		"-p",
		"--safe-mode",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", "",
		"--no-session-persistence",
	)
	command.Stdin = strings.NewReader(request)
	return command
}

func runClaudeUsageCommand(ctx context.Context, command *exec.Cmd) (json.RawMessage, error) {
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Claude Code usage request timed out: %w", ctx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return nil, fmt.Errorf("Claude Code usage request failed: %s", detail)
		}
		return nil, fmt.Errorf("Claude Code usage request failed: %w", err)
	}

	return parseClaudeBinUsageOutput(stdout.Bytes())
}

// claudeProxiedUsageError deliberately discards native stderr, stdout and
// control-error text: the proxied child environment embeds the scoped token in
// HTTPS_PROXY, so a CLI that echoes its environment or a remote error payload
// must never reach the terminal. Context cancellation stays actionable.
func claudeProxiedUsageError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("Claude Code proxy usage request canceled or timed out: %w", err)
	}
	return errors.New("Claude Code native usage failed through proxy; check Claude version, central account login and route grant")
}

func parseClaudeBinUsageOutput(output []byte) (json.RawMessage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var envelope claudeBinUsageEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || envelope.Type != "control_response" || envelope.Response.RequestID != claudeBinUsageRequestID {
			continue
		}
		if envelope.Response.Subtype == "error" {
			return nil, fmt.Errorf("Claude Code rejected the usage request: %s (update Claude Code if get_usage is unsupported)", envelope.Response.Error)
		}
		if envelope.Response.Subtype != "success" || len(envelope.Response.Response) == 0 {
			return nil, errors.New("Claude Code returned an empty usage response")
		}
		return envelope.Response.Response, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Claude Code usage response: %w", err)
	}
	return nil, errors.New("Claude Code did not return usage data (update Claude Code if get_usage is unsupported)")
}

func withoutEnvironmentVariable(environment []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func writeClaudeBinUsage(out io.Writer, report claudeBinUsageReport, now time.Time) error {
	return writeClaudeBinUsageWithOptions(out, report, now, providerusage.FormatOptions{})
}

func writeClaudeBinUsageWithOptions(out io.Writer, report claudeBinUsageReport, now time.Time, opts providerusage.FormatOptions) error {
	return writeClaudeBinUsageWithSource(out, report, now, opts, "Live from Claude Code")
}

func writeClaudeBinUsageWithSource(out io.Writer, report claudeBinUsageReport, now time.Time, opts providerusage.FormatOptions, source string) error {
	providerReport, err := claudeBinProviderReport(report, source)
	switch {
	case errors.Is(err, errClaudeBinUsageUnavailable):
		fmt.Fprintln(out, "Claude")
		if report.SubscriptionType != nil && strings.TrimSpace(*report.SubscriptionType) != "" {
			fmt.Fprintf(out, "%s plan · %s\n", titleCaseASCII(*report.SubscriptionType), source)
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out, source)
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, "Plan usage is unavailable.")
		fmt.Fprintln(out, "Claude Code needs claude.ai subscription OAuth with the user:profile scope;")
		fmt.Fprintln(out, "API-key and third-party-provider sessions do not expose plan limits.")
		return nil
	case errors.Is(err, errClaudeBinNoUsageWindows):
		fmt.Fprintln(out, "Claude")
		fmt.Fprintln(out, source)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "No active usage windows were returned.")
		return nil
	case err != nil:
		return err
	}
	_, err = fmt.Fprintln(out, providerusage.FormatWithOptions(providerReport, now, opts))
	return err
}

// claudeBinProviderReport returns a sentinel error when Claude reports no plan limits.
func claudeBinProviderReport(report claudeBinUsageReport, source string) (*providerusage.Report, error) {
	if !report.RateLimitsAvailable || report.RateLimits == nil {
		return nil, errClaudeBinUsageUnavailable
	}
	rows := claudeBinUsageRows(*report.RateLimits)
	if len(rows) == 0 {
		return nil, errClaudeBinNoUsageWindows
	}
	providerReport := &providerusage.Report{Provider: "claude", Source: source}
	if report.SubscriptionType != nil {
		providerReport.Plan = strings.TrimSpace(*report.SubscriptionType)
	}
	providerReport.Limits = make([]providerusage.Limit, 0, len(rows))
	for _, row := range rows {
		providerReport.Limits = append(providerReport.Limits, claudeBinProviderUsageLimit(row))
	}
	return providerReport, nil
}

func claudeBinProviderUsageLimit(row claudeBinUsageRow) providerusage.Limit {
	window := &providerusage.Window{
		Label:           row.WindowLabel,
		DurationMinutes: row.DurationMinutes,
		Detail:          row.Detail,
	}
	if row.Utilization != nil {
		window.UsedPercent = *row.Utilization
	}
	if row.ResetsAt != nil && strings.TrimSpace(*row.ResetsAt) != "" {
		if reset, err := time.Parse(time.RFC3339, *row.ResetsAt); err == nil {
			window.ResetsAt = reset
		} else {
			resetDetail := "Resets " + strings.TrimSpace(*row.ResetsAt)
			if window.Detail == "" {
				window.Detail = resetDetail
			} else {
				window.Detail += " · " + resetDetail
			}
		}
	}
	return providerusage.Limit{ID: strings.ToLower(strings.ReplaceAll(row.Label, " ", "_")), Name: row.Label, PrimaryWindow: window}
}

func claudeBinUsageRows(limits claudeBinRateLimits) []claudeBinUsageRow {
	rows := make([]claudeBinUsageRow, 0, 8)
	seen := make(map[string]bool)
	add := func(label, windowLabel string, durationMinutes int, limit *claudeBinRateLimit) {
		if limit != nil && limit.Utilization != nil {
			rows = append(rows, claudeBinUsageRow{
				Label:           label,
				WindowLabel:     windowLabel,
				DurationMinutes: durationMinutes,
				Utilization:     limit.Utilization,
				ResetsAt:        limit.ResetsAt,
			})
			seen[strings.ToLower(label)] = true
		}
	}
	add("Current session", "5 hour window", 5*60, limits.FiveHour)
	add("Current week · all models", "1 week window", 7*24*60, limits.SevenDay)
	add("Current week · OAuth apps", "1 week window", 7*24*60, limits.SevenDayOAuthApps)
	add("Current week · Opus", "1 week window", 7*24*60, limits.SevenDayOpus)
	add("Current week · Sonnet", "1 week window", 7*24*60, limits.SevenDaySonnet)
	for _, limit := range limits.ModelScoped {
		label := "Current week · " + limit.DisplayName
		if limit.Utilization == nil || seen[strings.ToLower(label)] {
			continue
		}
		rows = append(rows, claudeBinUsageRow{
			Label:           label,
			WindowLabel:     "1 week window",
			DurationMinutes: 7 * 24 * 60,
			Utilization:     limit.Utilization,
			ResetsAt:        limit.ResetsAt,
		})
		seen[strings.ToLower(label)] = true
	}
	if extra := limits.ExtraUsage; extra != nil && extra.IsEnabled && extra.Utilization != nil {
		detail := "Extra usage enabled"
		if extra.UsedCredits != nil && extra.MonthlyLimit != nil {
			currency := "USD"
			if extra.Currency != nil && *extra.Currency != "" {
				currency = *extra.Currency
			}
			detail = fmt.Sprintf("%s of %s", formatUsageCurrency(currency, *extra.UsedCredits), formatUsageCurrency(currency, *extra.MonthlyLimit))
		}
		rows = append(rows, claudeBinUsageRow{
			Label:       "Extra usage · this month",
			WindowLabel: "Monthly allowance",
			Utilization: extra.Utilization,
			Detail:      detail,
		})
	}
	return rows
}

func titleCaseASCII(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func formatUsageCurrency(currency string, value float64) string {
	symbols := map[string]string{
		"USD": "$",
		"EUR": "€",
		"GBP": "£",
		"JPY": "¥",
		"AUD": "A$",
		"CAD": "CA$",
		"NZD": "NZ$",
		"SGD": "S$",
	}
	if symbol := symbols[strings.ToUpper(currency)]; symbol != "" {
		return symbol + formatUsageAmount(value)
	}
	return strings.ToUpper(currency) + " " + formatUsageAmount(value)
}

func formatUsageAmount(value float64) string {
	if value == math.Trunc(value) {
		return strconv.FormatFloat(value, 'f', 0, 64)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}
