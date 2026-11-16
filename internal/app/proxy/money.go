package proxy

import (
	"fmt"
	"math/big"
	"regexp"
	"time"
)

// Money uses integer millionths of an ISO currency. Amount is absent for unknown,
// plan and local usage. Persist this snapshot, not a recalculation at read time.
type Money struct {
	Rate     *MoneyRate `json:"rate,omitempty"`
	Billing  string     `json:"billing"`
	Kind     string     `json:"kind"`
	Amount   *int64     `json:"amount_micros,omitempty"`
	Currency string     `json:"currency,omitempty"`
	Source   string     `json:"source,omitempty"`
	AsOf     string     `json:"asof,omitempty"`
	Reason   string     `json:"reason,omitempty"`
}

// MoneyInput is deliberately independent of the wire usage parser and Record.
// InputTokens excludes cache reads/writes; OutputTokens includes reasoning only
// when the provider includes it in its billable output count. Never add it twice.
type MoneyInput struct {
	Billing, Provider, Model, Tier                               string
	UsageState                                                   string // reported, partial, unknown
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	Charge                                                       *MoneyCharge
	RefusedBeforeDispatch                                        bool // not an inferred zero from an HTTP error
}
type MoneyCharge struct {
	AmountMicros           int64
	Currency, Source, AsOf string
}

// MoneyRate is an explicit operator price card, not a fuzzy model lookup.
// All prices are micros per million tokens. Input/output prices are required;
// nil is not a free rate. Cache prices must be explicitly
// supplied even when zero; nil means the cache semantics are not priced.
type MoneyRate struct {
	Provider   string `json:"provider" yaml:"provider"`
	Model      string `json:"model" yaml:"model"`
	Tier       string `json:"tier" yaml:"tier"`
	Currency   string `json:"currency" yaml:"currency"`
	Source     string `json:"source" yaml:"source"`
	AsOf       string `json:"asof" yaml:"asof"`
	Input      *int64 `json:"input_micros_per_million" yaml:"input_micros_per_million"`
	Output     *int64 `json:"output_micros_per_million" yaml:"output_micros_per_million"`
	CacheRead  *int64 `json:"cache_read_micros_per_million,omitempty" yaml:"cache_read_micros_per_million,omitempty"`
	CacheWrite *int64 `json:"cache_write_micros_per_million,omitempty" yaml:"cache_write_micros_per_million,omitempty"`
}

const maxMoneyMicros int64 = 9007199254740991 // exact in JSON consumers
var moneyCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

func validMoneyDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }
func validMoneyMetadata(currency, source, asof string) bool {
	return moneyCurrency.MatchString(currency) && source != "" && validMoneyDate(asof)
}

// ValidateMoneyRates rejects ambiguous and undated operator price cards. A
// historical or long-context price requires its own exact tier key; no fallback.
func ValidateMoneyRates(rates []MoneyRate) error {
	seen := map[[3]string]bool{}
	for _, r := range rates {
		key := [3]string{r.Provider, r.Model, r.Tier}
		if seen[key] || r.Provider == "" || r.Model == "" || r.Input == nil || r.Output == nil || !validMoneyMetadata(r.Currency, r.Source, r.AsOf) {
			return fmt.Errorf("money rate requires unique provider/model/tier and dated currency/source")
		}
		seen[key] = true
		for _, n := range []*int64{r.Input, r.Output, r.CacheRead, r.CacheWrite} {
			if n != nil && (*n < 0 || *n > maxMoneyMicros) {
				return fmt.Errorf("money rate out of range")
			}
		}
	}
	return nil
}

// CalculateMoney prefers provider charges over estimates. It never prices plans,
// local or unspecified billing, never guesses a model/tier/provider or cache rate,
// and rounds an estimate once, up to the nearest micro, using integer arithmetic.
func CalculateMoney(in MoneyInput, rates []MoneyRate) Money {
	m := Money{Billing: in.Billing, Kind: "unmeasured"}
	if m.Billing != "plan" && m.Billing != "local" && m.Billing != "pay_as_you_go" {
		m.Billing = "unspecified"
	}
	if in.RefusedBeforeDispatch {
		m.Kind = "no_charge_expected"
		m.Reason = "Blocked before any upstream dispatch."
		return m
	}
	switch m.Billing {
	case "plan":
		m.Reason = "Plan allowance and possible extra usage are not reported; configured plan traffic does not establish zero marginal billing."
		if in.UsageState == "reported" {
			m.Kind = "included"
		} else {
			m.Reason = "Plan activity could not be fully measured. " + m.Reason
		}
		return m
	case "local":
		m.Kind = "none"
		return m
	case "pay_as_you_go":
	default:
		m.Billing = "unspecified"
		m.Kind = "unspecified"
		m.Reason = "Tell us how this provider is billed; excluded from spending."
		return m
	}
	if c := in.Charge; c != nil {
		if c.AmountMicros >= 0 && c.AmountMicros <= maxMoneyMicros && validMoneyMetadata(c.Currency, c.Source, c.AsOf) {
			n := c.AmountMicros
			m.Kind, m.Amount, m.Currency, m.Source, m.AsOf = "charged", &n, c.Currency, c.Source, c.AsOf
			return m
		}
		m.Reason = "Provider charge has invalid or missing currency/source/date."
		return m
	}
	if in.UsageState != "reported" && in.UsageState != "partial" {
		m.Reason = "Usage not reported."
		return m
	}
	m.Kind = "unpriced"
	m.Reason = "No exact dated provider/model/tier price."
	if ValidateMoneyRates(rates) != nil {
		return m
	}
	for _, r := range rates {
		if r.Provider != in.Provider || r.Model != in.Model || r.Tier != in.Tier {
			continue
		}
		n, ok := moneyTokenCost(in, r)
		if !ok {
			m.Reason = "Usage or cache pricing is incomplete or out of range."
			return m
		}
		if in.UsageState == "partial" && n == 0 {
			m.Kind = "unmeasured"
			m.Reason = "Partial usage did not establish a positive priced lower bound."
			return m
		}
		m.Kind = "estimated"
		if in.UsageState == "partial" {
			m.Kind = "estimated_floor"
		}
		m.Amount, m.Currency, m.Source, m.AsOf, m.Reason = &n, r.Currency, r.Source, r.AsOf, ""
		snapshot := r
		input, output := *r.Input, *r.Output
		snapshot.Input, snapshot.Output = &input, &output
		if r.CacheRead != nil {
			v := *r.CacheRead
			snapshot.CacheRead = &v
		}
		if r.CacheWrite != nil {
			v := *r.CacheWrite
			snapshot.CacheWrite = &v
		}
		m.Rate = &snapshot
		return m
	}
	return m
}
func moneyTokenCost(in MoneyInput, r MoneyRate) (int64, bool) {
	total := new(big.Int)
	counts := []int64{in.InputTokens, in.OutputTokens, in.CacheReadTokens, in.CacheWriteTokens}
	prices := []*int64{r.Input, r.Output, r.CacheRead, r.CacheWrite}
	for i, count := range counts {
		if count < 0 {
			return 0, false
		}
		if count == 0 {
			continue
		}
		if prices[i] == nil {
			return 0, false
		}
		term := new(big.Int).Mul(big.NewInt(count), big.NewInt(*prices[i]))
		total.Add(total, term)
	}
	total.Add(total, big.NewInt(999999))
	total.Div(total, big.NewInt(1000000))
	if !total.IsInt64() || total.Int64() > maxMoneyMicros {
		return 0, false
	}
	return total.Int64(), true
}
