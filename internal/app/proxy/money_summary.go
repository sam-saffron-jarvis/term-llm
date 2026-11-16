package proxy

import (
	"fmt"
	"math/big"
	"sort"
	"time"
)

// MoneySettings is optional. Budgets are warnings, never admission limits.
// The owner must persist these and validate under existing authenticated/CSRF
// routing. No exchange conversion or subscription fee is inferred.
type MoneySettings struct {
	Timezone string       `json:"timezone" yaml:"timezone"`
	Budget   *MoneyBudget `json:"budget,omitempty" yaml:"budget,omitempty"`
	Rates    []MoneyRate  `json:"rates,omitempty" yaml:"rates,omitempty"`
}
type MoneyBudget struct {
	Currency    string `json:"currency" yaml:"currency"`
	LimitMicros int64  `json:"limit_micros" yaml:"limit_micros"`
}

func ValidateMoneySettings(s MoneySettings) error {
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("money timezone: %w", err)
		}
	}
	if b := s.Budget; b != nil && (!moneyCurrency.MatchString(b.Currency) || b.LimitMicros < 0 || b.LimitMicros > maxMoneyMicros) {
		return fmt.Errorf("invalid warning budget")
	}
	return ValidateMoneyRates(s.Rates)
}

// MoneyRecord is a frozen finish-time snapshot mapped by the owner from request
// JSON. App is explicit attribution, never inferred from prompt or User-Agent.
type MoneyRecord struct {
	Started                         time.Time
	DeviceID, Device, App, Provider string
	Paused                          bool
	Money                           Money
	Usage                           *ObservedUsage `json:"usage,omitempty"`
}
type MoneyTotal struct {
	Currency  string             `json:"currency"`
	Charged   int64              `json:"charged_micros"`
	Estimated int64              `json:"estimated_micros"`
	Floor     int64              `json:"floor_micros"`
	Total     int64              `json:"total_micros"`
	Qualifier string             `json:"qualifier"`
	Sources   []MoneyPriceSource `json:"sources"`
}
type MoneyPriceSource struct {
	Source string `json:"source"`
	AsOf   string `json:"asof"`
	Kind   string `json:"kind"`
}
type MoneyPeriod struct {
	PlanUnmeasured   int          `json:"plan_unmeasured"`
	Totals           []MoneyTotal `json:"totals"`
	Requests         int          `json:"requests"`
	Included         int          `json:"included"`
	Local            int          `json:"local"`
	Unpriced         int          `json:"unpriced"`
	Unmeasured       int          `json:"unmeasured"`
	Unspecified      int          `json:"unspecified"`
	NoChargeExpected int          `json:"no_charge_expected"`
}
type MoneyBreakdown struct {
	Name   string      `json:"name"`
	Paused bool        `json:"paused"`
	Period MoneyPeriod `json:"period"`
}
type MoneyBudgetStatus struct {
	Currency        string `json:"currency"`
	LimitMicros     int64  `json:"limit_micros"`
	RemainingMicros int64  `json:"remaining_micros"`
	UsedPercent     int    `json:"used_percent"`
	WarningOnly     bool   `json:"warning_only"`
	Incomplete      bool   `json:"incomplete"`
}
type MoneySummary struct {
	AsOf       time.Time          `json:"asof"`
	Timezone   string             `json:"timezone"`
	MonthLabel string             `json:"month_label"`
	Month      MoneyPeriod        `json:"month"`
	Today      MoneyPeriod        `json:"today"`
	Budget     *MoneyBudgetStatus `json:"budget,omitempty"`
	Devices    []MoneyBreakdown   `json:"devices"`
	Apps       []MoneyBreakdown   `json:"apps"`
	Providers  []MoneyBreakdown   `json:"providers"`
}

// BuildMoneySummary summarizes all supplied frozen rows for this calendar month.
// Caller must supply complete history (or durable rollup equivalents), not a
// paginated or retention-truncated request list. Unknown, corrupt and overflowing
// evidence remains unmeasured, never zero. Future rows are excluded; timezone defaults UTC.
func BuildMoneySummary(now time.Time, settings MoneySettings, records []MoneyRecord) (MoneySummary, error) {
	builder, err := newMoneySummaryBuilder(now, settings)
	if err != nil {
		return MoneySummary{}, err
	}
	for _, record := range records {
		if err := builder.add(record); err != nil {
			return MoneySummary{}, err
		}
	}
	return builder.finish(), nil
}

// The store feeds this accumulator directly from SQL rows. Memory is proportional
// to attribution groups and price sources, not monthly request count.
type moneySummaryBuilder struct {
	summary           MoneySummary
	settings          MoneySettings
	now, month, today time.Time
	groups            []map[string]*MoneyBreakdown
}

func newMoneySummaryBuilder(now time.Time, settings MoneySettings) (*moneySummaryBuilder, error) {
	if err := ValidateMoneySettings(settings); err != nil {
		return nil, err
	}
	zone := settings.Timezone
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	local := now.In(loc)
	return &moneySummaryBuilder{
		now: now, settings: settings,
		month:   time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc),
		today:   time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc),
		summary: MoneySummary{AsOf: now, Timezone: zone, MonthLabel: local.Format("January 2006"), Month: emptyMoneyPeriod(), Today: emptyMoneyPeriod(), Devices: []MoneyBreakdown{}, Apps: []MoneyBreakdown{}, Providers: []MoneyBreakdown{}},
		groups:  []map[string]*MoneyBreakdown{{}, {}, {}},
	}, nil
}
func (b *moneySummaryBuilder) add(r MoneyRecord) error {
	if r.Started.Before(b.month) || r.Started.After(b.now) {
		return nil
	}
	if err := addMoneyRecord(&b.summary.Month, r.Money); err != nil {
		return err
	}
	if !r.Started.Before(b.today) {
		if err := addMoneyRecord(&b.summary.Today, r.Money); err != nil {
			return err
		}
	}
	return addMoneyBreakdowns(b.groups, r)
}
func (b *moneySummaryBuilder) finish() MoneySummary {
	s := &b.summary
	finalizeMoneyPeriod(&s.Month)
	finalizeMoneyPeriod(&s.Today)
	lists := []*[]MoneyBreakdown{&s.Devices, &s.Apps, &s.Providers}
	for i, group := range b.groups {
		keys := make([]string, 0, len(group))
		for key := range group {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			row := group[key]
			finalizeMoneyPeriod(&row.Period)
			*lists[i] = append(*lists[i], *row)
		}
	}
	if b.settings.Budget != nil {
		s.Budget = buildMoneyBudget(*b.settings.Budget, s.Month)
	}
	return *s
}
func addMoneyBreakdowns(groups []map[string]*MoneyBreakdown, r MoneyRecord) error {
	deviceKey := r.DeviceID
	if deviceKey == "" {
		deviceKey = r.Device
	}
	names := []string{r.Device, r.App, r.Provider}
	keys := []string{deviceKey, r.App, r.Provider}
	for i, g := range groups {
		key := keys[i]
		name := names[i]
		if name == "" {
			name = "Not specified"
		}
		row := g[key]
		if row == nil {
			row = &MoneyBreakdown{Name: name, Period: emptyMoneyPeriod()}
			g[key] = row
		}
		if i == 0 {
			row.Paused = row.Paused || r.Paused
		}
		if err := addMoneyRecord(&row.Period, r.Money); err != nil {
			return err
		}
	}
	return nil
}
func buildMoneyBudget(b MoneyBudget, month MoneyPeriod) *MoneyBudgetStatus {
	var spent int64
	for _, t := range month.Totals {
		if t.Currency == b.Currency {
			spent = t.Total
		}
	}
	percent := 0
	if b.LimitMicros > 0 { // Bound before multiplying; no integer overflow.
		if spent >= b.LimitMicros {
			percent = 100
		} else {
			ratio := new(big.Int).Mul(big.NewInt(spent), big.NewInt(100))
			ratio.Div(ratio, big.NewInt(b.LimitMicros))
			percent = int(ratio.Int64())
		}
	} else if spent > 0 {
		percent = 100
	}
	result := &MoneyBudgetStatus{Currency: b.Currency, LimitMicros: b.LimitMicros, RemainingMicros: b.LimitMicros - spent, UsedPercent: percent, WarningOnly: true, Incomplete: moneyUnknown(month) > 0 || month.PlanUnmeasured > 0}
	for _, t := range month.Totals {
		if t.Currency != b.Currency || t.Qualifier == "at_least" {
			result.Incomplete = true
		}
	}

	return result
}
func emptyMoneyPeriod() MoneyPeriod  { return MoneyPeriod{Totals: []MoneyTotal{}} }
func moneyUnknown(p MoneyPeriod) int { return p.Unpriced + p.Unmeasured + p.Unspecified }
func finalizeMoneyPeriod(p *MoneyPeriod) {
	sort.Slice(p.Totals, func(i, j int) bool { return p.Totals[i].Currency < p.Totals[j].Currency })
	for i := range p.Totals {
		t := &p.Totals[i]
		t.Qualifier = "charged"
		if t.Estimated > 0 {
			t.Qualifier = "about"
		}
		if t.Floor > 0 || moneyUnknown(*p) > 0 {
			t.Qualifier = "at_least"
		}
		// Zero-valued estimates still carry their estimate qualifier.
		for _, source := range t.Sources {
			if source.Kind == "estimated_floor" {
				t.Qualifier = "at_least"
			} else if source.Kind == "estimated" && t.Qualifier == "charged" {
				t.Qualifier = "about"
			}
		}
	}
}
func addMoneyRecord(p *MoneyPeriod, m Money) error {
	p.Requests++
	if m.Billing == "unspecified" && m.Kind == "unmeasured" {
		p.Unmeasured++
		return nil
	}
	if m.Kind == "no_charge_expected" {
		p.NoChargeExpected++
		return nil
	}
	// Recheck billing at aggregation so even an invalid legacy snapshot cannot
	// turn a subscription's API equivalent into a charge.
	switch m.Billing {
	case "plan":
		if m.Kind == "included" {
			p.Included++
		} else {
			p.PlanUnmeasured++
		}
		return nil
	case "local":
		p.Local++
		return nil
	case "pay_as_you_go":
	default:
		p.Unspecified++
		return nil
	}
	switch m.Kind {
	case "unpriced":
		p.Unpriced++
		return nil
	case "charged", "estimated", "estimated_floor":
	default:
		p.Unmeasured++
		return nil
	}
	if m.Amount == nil || *m.Amount < 0 || *m.Amount > maxMoneyMicros || !validMoneyMetadata(m.Currency, m.Source, m.AsOf) {
		p.Unmeasured++
		return nil
	}
	index := -1
	for i := range p.Totals {
		if p.Totals[i].Currency == m.Currency {
			index = i
			break
		}
	}
	if index < 0 {
		p.Totals = append(p.Totals, MoneyTotal{Currency: m.Currency, Sources: []MoneyPriceSource{}})
		index = len(p.Totals) - 1
	}
	t := &p.Totals[index]
	if t.Total > maxMoneyMicros-*m.Amount {
		p.Unmeasured++
		return nil
	}
	t.Total += *m.Amount
	switch m.Kind {
	case "charged":
		t.Charged += *m.Amount
	case "estimated":
		t.Estimated += *m.Amount
	case "estimated_floor":
		t.Floor += *m.Amount
	}
	source := MoneyPriceSource{Source: m.Source, AsOf: m.AsOf, Kind: m.Kind}
	for _, s := range t.Sources {
		if s == source {
			return nil
		}
	}
	t.Sources = append(t.Sources, source)
	return nil
}
