package proxy

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMoneySettingsStrictYAML(t *testing.T) {
	const source = `timezone: Australia/Sydney
budget:
  currency: AUD
  limit_micros: 40000000
rates:
  - provider: fixture
    model: exact
    tier: priority
    currency: USD
    source: dated operator card
    asof: "2026-11-16"
    input_micros_per_million: 2000000
    output_micros_per_million: 6000000
    cache_read_micros_per_million: 0
    cache_write_micros_per_million: 3000000
`
	var s MoneySettings
	decoder := yaml.NewDecoder(strings.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMoneySettings(s); err != nil {
		t.Fatal(err)
	}
	if s.Budget.LimitMicros != 40000000 || s.Rates[0].Input == nil || *s.Rates[0].Input != 2000000 || s.Rates[0].CacheRead == nil || *s.Rates[0].CacheRead != 0 {
		t.Fatalf("YAML prices lost: %+v", s)
	}
	encoded, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "limit_micros:") || !strings.Contains(string(encoded), "input_micros_per_million:") {
		t.Fatal(string(encoded))
	}
	decoder = yaml.NewDecoder(strings.NewReader(strings.Replace(source, "limit_micros:", "limitmicros:", 1)))
	decoder.KnownFields(true)
	if err = decoder.Decode(&s); err == nil {
		t.Fatal("misspelled money field accepted")
	}
}
