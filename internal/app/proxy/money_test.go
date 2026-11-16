package proxy

import (
	"testing"
	"time"
)

func moneyPtr(n int64) *int64 { return &n }
func testMoneyRate() MoneyRate {
	return MoneyRate{Provider: "fixture", Model: "exact", Currency: "USD", Source: "operator fixture", AsOf: "2026-11-01", Input: moneyPtr(2000000), Output: moneyPtr(6000000), CacheRead: moneyPtr(500000), CacheWrite: moneyPtr(3000000)}
}
func TestCalculateMoney(t *testing.T) {
	rate := testMoneyRate()
	base := MoneyInput{Billing: "pay_as_you_go", Provider: "fixture", Model: "exact", UsageState: "reported", InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 100, CacheWriteTokens: 50}
	tests := []struct {
		name   string
		change func(*MoneyInput)
		kind   string
		amount int64
	}{
		{"estimate", func(*MoneyInput) {}, "estimated", 3400},
		{"partial", func(i *MoneyInput) { i.UsageState = "partial" }, "estimated_floor", 3400},
		{"provider preferred", func(i *MoneyInput) {
			i.Charge = &MoneyCharge{AmountMicros: 123, Currency: "EUR", Source: "vendor", AsOf: "2026-11-15"}
		}, "charged", 123},
		{"plan never priced", func(i *MoneyInput) {
			i.Billing = "plan"
			i.Charge = &MoneyCharge{AmountMicros: 999, Currency: "USD", Source: "vendor", AsOf: "2026-11-15"}
		}, "included", -1},
		{"local", func(i *MoneyInput) { i.Billing = "local" }, "none", -1},
		{"unspecified", func(i *MoneyInput) { i.Billing = "" }, "unspecified", -1},
		{"no usage", func(i *MoneyInput) { i.UsageState = "unknown" }, "unmeasured", -1},
		{"unknown model", func(i *MoneyInput) { i.Model = "exact-new" }, "unpriced", -1},
		{"other provider", func(i *MoneyInput) { i.Provider = "aggregator" }, "unpriced", -1},
		{"other tier", func(i *MoneyInput) { i.Tier = "priority" }, "unpriced", -1},
		{"negative usage", func(i *MoneyInput) { i.InputTokens = -1 }, "unpriced", -1},
		{"refused", func(i *MoneyInput) { i.RefusedBeforeDispatch = true }, "no_charge_expected", -1},
		{"bad charge", func(i *MoneyInput) { i.Charge = &MoneyCharge{AmountMicros: 1, Currency: "$"} }, "unmeasured", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.change(&in)
			got := CalculateMoney(in, []MoneyRate{rate})
			if got.Kind != tt.kind {
				t.Fatalf("%+v", got)
			}
			if tt.amount < 0 {
				if got.Amount != nil || got.Currency != "" {
					t.Fatalf("non-money amount: %+v", got)
				}
			} else if got.Amount == nil || *got.Amount != tt.amount {
				t.Fatalf("amount: %+v", got)
			}
		})
	}
	t.Run("cache rate absent", func(t *testing.T) {
		rate.CacheRead = nil
		if got := CalculateMoney(base, []MoneyRate{rate}); got.Kind != "unpriced" {
			t.Fatal(got)
		}
	})
}
func TestMoneyRateValidation(t *testing.T) {
	t.Run("missing is not free", func(t *testing.T) {
		r := testMoneyRate()
		r.Input = nil
		if err := ValidateMoneyRates([]MoneyRate{r}); err == nil {
			t.Fatal("missing input price accepted as zero")
		}
	})
	r := testMoneyRate()
	if err := ValidateMoneyRates([]MoneyRate{r, r}); err == nil {
		t.Fatal("duplicate accepted")
	}
	r.AsOf = "yesterday"
	if err := ValidateMoneyRates([]MoneyRate{r}); err == nil {
		t.Fatal("undated accepted")
	}
	r = testMoneyRate()
	r.Input = moneyPtr(-1)
	if err := ValidateMoneyRates([]MoneyRate{r}); err == nil {
		t.Fatal("negative accepted")
	}
}
func TestMoneyIntegerRoundingAndOverflow(t *testing.T) {
	r := testMoneyRate()
	r.Input = moneyPtr(1)
	in := MoneyInput{Billing: "pay_as_you_go", Provider: r.Provider, Model: r.Model, UsageState: "reported", InputTokens: 1}
	m := CalculateMoney(in, []MoneyRate{r})
	if m.Amount == nil || *m.Amount != 1 {
		t.Fatal(m)
	}
	in.InputTokens = 1 << 62
	r.Input = moneyPtr(maxMoneyMicros)
	if got := CalculateMoney(in, []MoneyRate{r}); got.Kind != "unpriced" {
		t.Fatal(got)
	}
}
func TestMoneySummary(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	charged := Money{Billing: "pay_as_you_go", Kind: "charged", Amount: moneyPtr(3000000), Currency: "USD", Source: "vendor", AsOf: "2026-11-16"}
	estimated := charged
	estimated.Kind = "estimated"
	estimated.Amount = moneyPtr(2000000)
	eur := charged
	eur.Currency = "EUR"
	records := []MoneyRecord{
		{Started: now, DeviceID: "a", Device: "Laptop", App: "chat", Provider: "API", Money: charged},
		{Started: now.Add(-24 * time.Hour), DeviceID: "a", Device: "Laptop", App: "chat", Provider: "API", Money: estimated},
		{Started: now, DeviceID: "b", Device: "Laptop", Provider: "Other API", Money: eur, Paused: true},
		{Started: now, Provider: "Plan", Money: Money{Billing: "plan", Kind: "charged", Amount: moneyPtr(999999999)}},
		{Started: now, Money: Money{Billing: "pay_as_you_go", Kind: "unpriced"}},
		{Started: now, Money: Money{Billing: "unspecified"}},
		{Started: now.AddDate(0, -1, 0), Money: charged},
		{Started: now.Add(time.Hour), Money: charged},
	}
	s, err := BuildMoneySummary(now, MoneySettings{Budget: &MoneyBudget{Currency: "USD", LimitMicros: 10000000}}, records)
	if err != nil {
		t.Fatal(err)
	}
	if s.Month.Requests != 6 || s.Today.Requests != 5 || s.Month.Included != 0 || s.Month.PlanUnmeasured != 1 || s.Month.Unspecified != 1 {
		t.Fatalf("%+v", s)
	}
	if len(s.Month.Totals) != 2 || s.Month.Totals[1].Total != 5000000 || s.Month.Totals[1].Qualifier != "at_least" {
		t.Fatal(s.Month.Totals)
	}
	if s.Budget.RemainingMicros != 5000000 || !s.Budget.WarningOnly || !s.Budget.Incomplete || s.Budget.UsedPercent != 50 {
		t.Fatal(s.Budget)
	}
	if len(s.Devices) != 3 {
		t.Fatal(s.Devices)
	} // empty identity + distinct named devices
}
func TestMoneySummaryTimezoneAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC)
	s, err := BuildMoneySummary(now, MoneySettings{Timezone: "Australia/Sydney"}, nil)
	if err != nil || s.MonthLabel != "December 2026" || s.Month.Totals == nil {
		t.Fatalf("%+v %v", s, err)
	}
	_, err = BuildMoneySummary(now, MoneySettings{Timezone: "not/a/zone"}, nil)
	if err == nil {
		t.Fatal("invalid timezone")
	}
}
func TestMoneySummaryCorruptAndOverflow(t *testing.T) {
	now := time.Now()
	m := Money{Billing: "pay_as_you_go", Kind: "charged", Amount: moneyPtr(maxMoneyMicros), Currency: "USD", Source: "vendor", AsOf: "2026-11-16"}
	rows := []MoneyRecord{{Started: now, Money: m}, {Started: now, Money: m}}
	summary, err := BuildMoneySummary(now, MoneySettings{}, rows)
	if err != nil || summary.Month.Unmeasured != 1 || summary.Month.Totals[0].Qualifier != "at_least" {
		t.Fatalf("overflow must retain known spending and mark unknown: %+v %v", summary, err)
	}
	m.Amount = nil
	rows[0].Money = m
	summary, err = BuildMoneySummary(now, MoneySettings{}, rows[:1])
	if err != nil || summary.Month.Unmeasured != 1 || len(summary.Month.Totals) != 0 {
		t.Fatalf("missing amount must remain unmeasured: %+v %v", summary, err)
	}
}

func TestMoneyPartialZeroIsUnknown(t *testing.T) {
	r := testMoneyRate()
	m := CalculateMoney(MoneyInput{Billing: "pay_as_you_go", Provider: r.Provider, Model: r.Model, UsageState: "partial"}, []MoneyRate{r})
	if m.Amount != nil || m.Kind != "unmeasured" {
		t.Fatalf("partial became a zero bill: %+v", m)
	}
}
func TestMoneyBudgetFloorAndThreshold(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"charged", "estimated", "estimated_floor"} {
		t.Run(kind, func(t *testing.T) {
			m := Money{Billing: "pay_as_you_go", Kind: kind, Amount: moneyPtr(8000000), Currency: "USD", Source: "fixture", AsOf: "2026-11-16"}
			s, err := BuildMoneySummary(now, MoneySettings{Budget: &MoneyBudget{Currency: "USD", LimitMicros: 10000000}}, []MoneyRecord{{Started: now, Money: m}})
			if err != nil {
				t.Fatal(err)
			}
			if s.Budget.UsedPercent != 80 || s.Budget.RemainingMicros != 2000000 || s.Budget.Incomplete != (kind == "estimated_floor") {
				t.Fatal(s.Budget)
			}
		})
	}
}
func TestMoneyExactZeroEstimateQualifier(t *testing.T) {
	now := time.Now()
	m := Money{Billing: "pay_as_you_go", Kind: "estimated", Amount: moneyPtr(0), Currency: "USD", Source: "explicit free operator card", AsOf: "2026-11-16"}
	s, err := BuildMoneySummary(now, MoneySettings{}, []MoneyRecord{{Started: now, Money: m}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Month.Totals[0].Qualifier != "about" {
		t.Fatal(s.Month.Totals)
	}
}

func TestMoneyFreezesRateAndCacheSemantics(t *testing.T) {
	r := testMoneyRate()
	r.Tier = "priority-long-context"
	in := MoneyInput{Billing: "pay_as_you_go", Provider: r.Provider, Model: r.Model, Tier: r.Tier, UsageState: "reported", InputTokens: 100, CacheReadTokens: 100}
	rates := []MoneyRate{r}
	m := CalculateMoney(in, rates)
	*r.CacheRead = 9999999
	*rates[0].Input = 9999999
	if m.Rate == nil || m.Rate.Tier != in.Tier || *m.Rate.CacheRead != 500000 || *m.Rate.Input != 2000000 {
		t.Fatalf("rate not frozen: %+v", m.Rate)
	}
	if m.Amount == nil || *m.Amount != 250 {
		t.Fatalf("cache counts double counted: %+v", m)
	}
}

func TestMoneyPlanAndRefusalFinancialStates(t *testing.T) {
	for _, billing := range []string{"pay_as_you_go", "plan", "local", "unspecified", ""} {
		t.Run("blocked/"+billing, func(t *testing.T) {
			m := CalculateMoney(MoneyInput{Billing: billing, UsageState: "reported", RefusedBeforeDispatch: true, Charge: &MoneyCharge{AmountMicros: 999, Currency: "USD", Source: "irrelevant", AsOf: "2026-11-16"}}, nil)
			if m.Kind != "no_charge_expected" || m.Amount != nil || m.Rate != nil || m.Currency != "" {
				t.Fatalf("blocked work became activity or money: %+v", m)
			}
			now := time.Now()
			s, err := BuildMoneySummary(now, MoneySettings{}, []MoneyRecord{{Started: now, Money: m}})
			if err != nil || s.Month.NoChargeExpected != 1 || s.Month.Included != 0 || s.Month.Local != 0 || s.Month.PlanUnmeasured != 0 || s.Month.Unspecified != 0 || len(s.Month.Totals) != 0 {
				t.Fatalf("blocked summary: %+v, %v", s, err)
			}
		})
	}
	for _, state := range []string{"reported", "partial", "unknown", ""} {
		t.Run("plan/"+state, func(t *testing.T) {
			m := CalculateMoney(MoneyInput{Billing: "plan", UsageState: state, Charge: &MoneyCharge{AmountMicros: 999, Currency: "USD", Source: "API equivalent ignored", AsOf: "2026-11-16"}}, nil)
			expected := "unmeasured"
			if state == "reported" {
				expected = "included"
			}
			if m.Kind != expected || m.Amount != nil || m.Currency != "" || m.Rate != nil || m.Reason == "" {
				t.Fatalf("plan financial state: %+v", m)
			}
			now := time.Now()
			s, err := BuildMoneySummary(now, MoneySettings{}, []MoneyRecord{{Started: now, Money: m}})
			if err != nil || len(s.Month.Totals) != 0 || s.Month.Unmeasured != 0 {
				t.Fatalf("plan leaked into API totals: %+v %v", s, err)
			}
			if state == "reported" {
				if s.Month.Included != 1 || s.Month.PlanUnmeasured != 0 {
					t.Fatal(s.Month)
				}
			} else if s.Month.PlanUnmeasured != 1 || s.Month.Included != 0 {
				t.Fatal(s.Month)
			}
		})
	}
}

func TestMoneyMixedPlanUnknownHasDistinctCoverageAndOneCurrencyTotal(t *testing.T) {
	now := time.Now()
	charge := Money{Billing: "pay_as_you_go", Kind: "charged", Amount: moneyPtr(1000000), Currency: "USD", Source: "vendor", AsOf: "2026-11-16"}
	estimate := charge
	estimate.Kind = "estimated"
	estimate.Amount = moneyPtr(2000000)
	plan := charge
	plan.Billing = "plan"
	plan.Kind = "unmeasured"
	plan.Amount = moneyPtr(99000000)
	rows := []MoneyRecord{{Started: now, Money: charge}, {Started: now, Money: estimate}, {Started: now, Money: plan}}
	s, err := BuildMoneySummary(now, MoneySettings{}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Month.Totals) != 1 || s.Month.Totals[0].Currency != "USD" || s.Month.Totals[0].Total != 3000000 || s.Month.PlanUnmeasured != 1 || s.Month.Unmeasured != 0 || s.Month.Included != 0 {
		t.Fatalf("mixed financial picture: %+v", s.Month)
	}
}
