package proxy

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func offlineObserved(model, tier string, input, read, output int64) ObservedUsage {
	return ObservedUsage{Protocol: usagetap.Responses, Usage: usagetap.Usage{Model: model, Tier: tier, Input: input, CachedInput: read, CacheWrite: 0, Output: output, Reasoning: -1, Total: input + output, Source: usagetap.Reported, Turns: 1}}
}
func offlineMoneyInput(route Route, observed ObservedUsage) MoneyInput {
	return moneyInputFromUsage(MoneyInput{Billing: route.Billing, Provider: route.Name}, observed)
}
func TestMoneyOfflineKnownWithoutOperatorCard(t *testing.T) {
	for _, tier := range []string{"default"} {
		t.Run("tier="+tier, func(t *testing.T) {
			route := Route{Name: "household-work", Upstream: "https://api.openai.com/v1", Billing: "pay_as_you_go"}
			observed := offlineObserved("gpt-5.5", tier, 12400, 0, 850)
			observed.Usage.CacheWrite = -1 // Parser's absent-write sentinel; this tariff explicitly prices writes at zero.
			got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
			// Canonical 5 USD/M input + 30 USD/M output: 0.062 + 0.0255 USD.
			if got.Kind != "estimated" || got.Amount == nil || *got.Amount != 87500 || got.Currency != "USD" || got.AsOf != "2026-09-15" || !strings.Contains(got.Source, "developers.openai.com") || got.Rate == nil || got.Rate.Tier != tier {
				t.Fatalf("built-in demo arithmetic/evidence: %+v", got)
			}
		})
	}
	route := Route{Name: "arbitrary-name", Upstream: "https://api.openai.com/v1/", Billing: "pay_as_you_go"}
	observed := offlineObserved("gpt-5.6-terra", "default", 12400, 0, 850)
	// Nonzero write tariff is eligible only with explicitly known zero writes.
	got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
	if got.Amount == nil || *got.Amount != 35000 {
		t.Fatalf("Terra explicit zero-write price: %+v", got)
	}
}
func TestMoneyOfflineOperatorOverridesRemainAuthoritative(t *testing.T) {
	route := Route{Name: "openai-work", Upstream: "https://api.openai.com/v1", Billing: "pay_as_you_go"}
	observed := offlineObserved("gpt-5.6-terra", "default", 12400, 0, 850)
	input := offlineMoneyInput(route, observed)
	card := MoneyRate{Provider: route.Name, Model: input.Model, Tier: input.Tier, Currency: "EUR", Source: "SYNTHETIC operator card", AsOf: "2026-11-01", Input: moneyPtr(3000000), Output: moneyPtr(15000000)}
	got := finishRequestMoney(input, route, &observed, []MoneyRate{card})
	if got.Amount == nil || *got.Amount != 49950 || got.Currency != "EUR" || got.Source != card.Source || got.AsOf != card.AsOf {
		t.Fatalf("owner override lost: %+v", got)
	}
	observed.Usage.CachedInput = 100
	input = offlineMoneyInput(route, observed)
	got = finishRequestMoney(input, route, &observed, []MoneyRate{card})
	if got.Kind != "unpriced" || got.Amount != nil {
		t.Fatalf("bundled fallback filled an incomplete owner card: %+v", got)
	}
	// An exact different-tier card does not shadow the standard fallback.
	card.Tier = "priority"
	got = finishRequestMoney(input, route, &observed, []MoneyRate{card})
	if got.Kind != "estimated" || got.Currency != "USD" {
		t.Fatalf("unrelated tier blocked exact fallback: %+v", got)
	}
}
func TestMoneyOfflineDoesNotGuessUpstreamModelTierOrCache(t *testing.T) {
	base := Route{Name: "openai", Upstream: "https://api.openai.com/v1", Billing: "pay_as_you_go"}
	tests := []struct {
		name   string
		url    string
		change func(*ObservedUsage)
	}{
		{"custom host", "https://gateway.example/v1", nil},
		{"aggregator", "https://openrouter.ai/api/v1", nil},
		{"different port", "https://api.openai.com:8443/v1", nil},
		{"insecure", "http://api.openai.com/v1", nil},
		{"wrong path", "https://api.openai.com/proxy/v1", nil},
		{"escaped path", "https://api.openai.com/%76%31", nil},
		{"query", "https://api.openai.com/v1?price=default", nil},
		{"empty query", "https://api.openai.com/v1?", nil},
		{"userinfo", "https://user@api.openai.com/v1", nil},
		{"lookalike", "https://api.openai.com.example/v1", nil},
		{"unknown model", "", func(o *ObservedUsage) { o.Usage.Model = "gpt-new" }},
		{"fuzzy effort alias", "", func(o *ObservedUsage) { o.Usage.Model = "gpt-5.5-high" }},
		{"prefixed model", "", func(o *ObservedUsage) { o.Usage.Model = "openai/gpt-5.5" }},
		{"wrong provider model", "", func(o *ObservedUsage) { o.Usage.Model = "claude-opus-5" }},
		{"omitted tier", "", func(o *ObservedUsage) { o.Usage.Tier = "" }},
		{"priority", "", func(o *ObservedUsage) { o.Usage.Tier = "priority" }},
		{"auto", "", func(o *ObservedUsage) { o.Usage.Tier = "auto" }},
		{"flex", "", func(o *ObservedUsage) { o.Usage.Tier = "flex" }},
		{"ambiguous", "", func(o *ObservedUsage) { o.Usage.Ambiguous = true }},
		{"unknown read count", "", func(o *ObservedUsage) { o.Usage.CachedInput = -1 }},
		{"unknown write count with nonzero tariff", "", func(o *ObservedUsage) { o.Usage.Model = "gpt-5.6-terra"; o.Usage.CacheWrite = -1 }},
		{"read exceeds input", "", func(o *ObservedUsage) { o.Usage.CachedInput = 999999 }},
		{"unknown output", "", func(o *ObservedUsage) { o.Usage.Output = -1 }},
		{"separate cache writes", "", func(o *ObservedUsage) { o.Usage.CacheWrite = 100 }},
		{"wrong protocol", "", func(o *ObservedUsage) { o.Protocol = usagetap.Gemini }},
		{"multiple responses", "", func(o *ObservedUsage) { o.Usage.Turns = 2 }},
		{"missing request boundaries", "", func(o *ObservedUsage) { o.Usage.Turns = 0 }},
		{"partial below threshold", "", func(o *ObservedUsage) { o.Usage.Source = usagetap.Partial }},
		{"partial above threshold", "", func(o *ObservedUsage) { o.Usage.Source = usagetap.Partial; o.Usage.Input = 272001 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := base
			if tt.url != "" {
				route.Upstream = tt.url
			}
			observed := offlineObserved("gpt-5.5", "default", 12400, 0, 850)
			if tt.change != nil {
				tt.change(&observed)
			}
			got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
			if got.Amount != nil || got.Currency != "" || got.Rate != nil || got.Source != "" || got.AsOf != "" {
				t.Fatalf("guessed price/evidence: %+v", got)
			}
		})
	}
}
func TestMoneyOfflineWholeRequestThresholdIncludesCachedInput(t *testing.T) {
	route := Route{Name: "api", Upstream: "https://api.openai.com:443/v1/", Billing: "pay_as_you_go"}
	tests := []struct {
		name                      string
		input, read, output, want int64
	}{
		{"exact threshold base", 272000, 0, 100, 545200},
		{"one above all repriced", 272001, 0, 100, 1089804},
		{"threshold reached by cache", 272000, 72000, 100, 415600},
		{"cache crosses threshold", 272001, 72001, 100, 830601},
		{"entirely cached over threshold", 272001, 272001, 1, 108819},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observed := offlineObserved("gpt-5.6-terra", "default", tt.input, tt.read, tt.output)
			got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
			if got.Amount == nil || *got.Amount != tt.want {
				t.Fatalf("wrong whole-request/cached tier: %+v want %d", got, tt.want)
			}
			if tt.input > 272000 && (!strings.Contains(got.Source, "whole-request") || *got.Rate.Output != 18000000) {
				t.Fatal("output was not repriced with whole request", got)
			}
		})
	}
}
func TestMoneyOfflineDatedOpusDoesNotGuessWriteTTL(t *testing.T) {
	route := Route{Name: "claude-work", Upstream: "https://api.anthropic.com/v1", Billing: "pay_as_you_go"}
	observed := ObservedUsage{Protocol: usagetap.Messages, Usage: usagetap.Usage{Model: "claude-opus-5", Tier: "standard", Input: 1000, CachedInput: 100, CacheWrite: 0, Output: 200, Source: usagetap.Reported, Turns: 1}}
	got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
	if got.Amount == nil || *got.Amount != 10050 || got.AsOf != "2026-09-14" || got.Rate.CacheWrite != nil {
		t.Fatalf("Opus known read/zero-write arithmetic: %+v", got)
	}
	observed.Usage.CacheWrite = 100
	got = finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
	if got.Kind != "unpriced" || got.Amount != nil || !strings.Contains(got.Reason, "TTL") {
		t.Fatalf("unknown write TTL priced: %+v", got)
	}
	observed.Usage.CacheWrite = 0
	observed.Usage.Tier = ""
	if got = finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil); got.Amount != nil {
		t.Fatal("unattested Anthropic tier priced", got)
	}
}
func TestMoneyOfflineNeverPricesPlansAndStillPrefersProviderCharge(t *testing.T) {
	route := Route{Name: "work", Upstream: "https://api.openai.com/v1", Billing: "plan"}
	observed := offlineObserved("gpt-5.5", "default", 12400, 0, 850)
	input := offlineMoneyInput(route, observed)
	got := finishRequestMoney(input, route, &observed, nil)
	if got.Kind != "included" || got.Amount != nil || got.Currency != "" || got.Rate != nil {
		t.Fatal("plan given API equivalent", got)
	}
	route.Billing = "pay_as_you_go"
	input = offlineMoneyInput(route, observed)
	input.Charge = &MoneyCharge{AmountMicros: 123, Currency: "EUR", Source: "actual provider request charge", AsOf: "2026-11-16"}
	got = finishRequestMoney(input, route, &observed, nil)
	if got.Kind != "charged" || got.Amount == nil || *got.Amount != 123 || got.Rate != nil {
		t.Fatal("provider charge was estimated or double-counted", got)
	}
}
func TestMoneyOfflineFinishIntegrationUsesActualUpstreamNotRouteName(t *testing.T) {
	s := testService(t)
	route := Route{Name: "household", Billing: "pay_as_you_go", Upstream: "https://api.openai.com/v1"}
	observed := offlineObserved("gpt-5.6-terra", "default", 12400, 0, 850)
	record := Record{Outcome: "complete", Usage: &observed}
	s.finalizeMoney(&record, route)
	if record.Money == nil || record.Money.Amount == nil || *record.Money.Amount != 35000 {
		t.Fatalf("finish did not select bundled dated pricing: %+v", record.Money)
	}
	route.Name = "openai"
	route.Upstream = "https://aggregator.example/v1"
	s.finalizeMoney(&record, route)
	if record.Money.Amount != nil || record.Money.Kind != "unpriced" {
		t.Fatal("route name established false first-party identity", record.Money)
	}
}

func TestMoneyOfflineActualResponsesFixtureCounters(t *testing.T) {
	route := Route{Name: "fixture-with-first-party-identity", Upstream: "https://api.openai.com/v1", Billing: "pay_as_you_go"}
	parser := usagetap.New(usagetap.Responses, usagetap.SSE)
	parser.Feed([]byte("data: " + `{"type":"response.completed","response":{"id":"synthetic-bundled","model":"gpt-5.5","service_tier":"default","usage":{"input_tokens":12400,"input_tokens_details":{"cached_tokens":2400},"output_tokens":850,"output_tokens_details":{"reasoning_tokens":200},"total_tokens":13250}}}` + "\n\n"))
	observed := ObservedUsage{Protocol: usagetap.Responses, Usage: parser.Finish(true)}
	got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
	// Fresh: 10,000 * 5 USD/M; reads: 2,400 * .50 USD/M;
	// output: 850 * 30 USD/M (already includes reasoning, do not add 200).
	if got.Amount == nil || *got.Amount != 76700 || got.AsOf != "2026-09-15" {
		t.Fatalf("real parser -> offline tariff conventions: usage=%+v money=%+v", observed, got)
	}
}

func TestMoneyOfflineAnthropicPresetRootAndRealNestedTier(t *testing.T) {
	var preset routePreset
	for _, candidate := range routePresets {
		if candidate.ID == "anthropic" {
			preset = candidate
			break
		}
	}
	if preset.Upstream != "https://api.anthropic.com" {
		t.Fatalf("unexpected canonical preset: %+v", preset)
	}
	for _, format := range []usagetap.Format{usagetap.JSON, usagetap.SSE} {
		for _, tier := range []string{"standard", "priority", ""} {
			t.Run(string(format)+"/"+tier, func(t *testing.T) {
				nested := ""
				if tier != "" {
					nested = `,"service_tier":"` + tier + `"`
				}
				message := `{"type":"message","model":"claude-opus-5","usage":{"input_tokens":1000,"cache_read_input_tokens":100,"cache_creation_input_tokens":0,"output_tokens":200` + nested + `}}`
				parser := usagetap.New(usagetap.Messages, format)
				if format == usagetap.SSE {
					parser.Feed([]byte("data: " + `{"type":"message_start","message":` + message + "}\n\n"))
				} else {
					parser.Feed([]byte(message))
				}
				observed := ObservedUsage{Protocol: usagetap.Messages, Usage: parser.Finish(true)}
				route := Route{Name: "household-claude", Upstream: preset.Upstream, Billing: preset.Billing}
				got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
				if tier == "standard" {
					if got.Kind != "estimated" || got.Amount == nil || *got.Amount != 10050 || got.Currency != "USD" || got.AsOf != "2026-09-14" || !strings.Contains(got.Source, "Anthropic") {
						t.Fatalf("preset + parser + dated Opus price: %+v", got)
					}
				} else if got.Kind != "unpriced" || got.Amount != nil {
					t.Fatalf("unattested/unsupported tier priced: %+v", got)
				}
			})
		}
	}
	// Actual nested-tier Messages evidence still does not attest write TTL.
	parser := usagetap.New(usagetap.Messages, usagetap.JSON)
	parser.Feed([]byte(`{"model":"claude-opus-5","usage":{"input_tokens":1000,"cache_read_input_tokens":100,"cache_creation_input_tokens":100,"output_tokens":200,"service_tier":"standard"}}`))
	observed := ObservedUsage{Protocol: usagetap.Messages, Usage: parser.Finish(true)}
	route := Route{Name: "claude", Upstream: preset.Upstream, Billing: preset.Billing}
	got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
	if got.Kind != "unpriced" || got.Amount != nil || !strings.Contains(got.Reason, "TTL") {
		t.Fatalf("nested standard tier invented a cache-write TTL: %+v", got)
	}
	for _, root := range []string{"https://api.anthropic.com", "https://api.anthropic.com/", "https://api.anthropic.com/v1", "https://api.anthropic.com/v1/", "https://api.anthropic.com:443/"} {
		if provider := offlinePriceProvider(root); provider != "anthropic" {
			t.Fatalf("canonical Anthropic base rejected: %s", root)
		}
	}
	for _, root := range []string{"https://api.anthropic.com/custom", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com/custom/v1", "https://api.anthropic.com:8443", "http://api.anthropic.com", "https://aggregator.example/", "https://api.openai.com", "https://api.openai.com/"} {
		if provider := offlinePriceProvider(root); provider != "" {
			t.Fatalf("noncanonical base guessed: %s -> %s", root, provider)
		}
	}
}
func TestMoneyOfflineExactCodexAndPreparedGPT55Demo(t *testing.T) {
	route := Route{Name: "api", Upstream: "https://api.openai.com/v1", Billing: "pay_as_you_go"}
	for _, tt := range []struct {
		model string
		want  int64
	}{{"gpt-5.3-codex", 30450}, {"gpt-5.5", 78500}} {
		observed := offlineObserved(tt.model, "default", 12400, 2000, 850)
		observed.Usage.CacheWrite = -1 // Both canonical tariffs explicitly price writes at zero.
		got := finishRequestMoney(offlineMoneyInput(route, observed), route, &observed, nil)
		if got.Amount == nil || *got.Amount != tt.want || got.AsOf != "2026-09-15" {
			t.Fatalf("%s exact canonical price: %+v", tt.model, got)
		}
		if tt.model == "gpt-5.5" && *got.Amount*4 != 314000 {
			t.Fatal("prepared four-request Mini arithmetic drifted")
		}
	}
}
