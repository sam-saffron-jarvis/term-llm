package proxy

import (
	"cmp"
	"strings"

	"github.com/term-llm/term-llm/internal/app/proxy/moneytimeline"
	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

type timelinePlanCost struct {
	micros   int64
	complete int64 // Floors are already counted in unknownCost.
}
type timelineAggregate struct {
	label     string
	cost      map[string]int64
	estimated map[string]bool
	// Keep plan estimates separate so they cannot displace existing plotted costs on overflow.
	planCost                         map[string]timelinePlanCost
	tokens                           int64
	tokenParts                       moneytimeline.TokenParts
	tokenKnown                       bool
	unknownCost, unknownTokens, plan int64
}

func newTimelineAggregate(label string) *timelineAggregate {
	return &timelineAggregate{label: label, cost: map[string]int64{}, estimated: map[string]bool{}}
}

func timelineGroupIdentity(rec MoneyRecord, caller, route, group string) (string, string) {
	if group == "client" {
		key := cmp.Or(rec.DeviceID, caller)
		label := cmp.Or(rec.Device, "Unknown client")
		if rec.Tag != "" {
			return key + "\x00" + rec.Tag, label + " · " + rec.Tag
		}
		return key, label
	}
	if group == "provider" {
		// Label by the configured name: brand names collide across distinct
		// providers (anthropic and claude-bin, or two Anthropic accounts).
		key := cmp.Or(rec.Provider, route)
		return key, cmp.Or(strings.TrimSpace(key), "Unknown provider")
	}
	if group == "model" {
		model := timelineModel(rec)
		return "model:" + model, cmp.Or(model, "Unknown model")
	}
	return "total", "Total"
}

func timelineModel(rec MoneyRecord) string {
	if rec.Usage != nil {
		if rec.Usage.Usage.Ambiguous {
			return ""
		}
		if model := rec.Usage.Usage.Model; model != "" {
			return model
		}
	}
	// A stored plan estimate can preserve the response identity when the
	// original usage snapshot is no longer available.
	if equivalent := rec.Money.APIEquivalent; equivalent != nil {
		return equivalent.Model
	}
	return ""
}

func supportedTimelineProtocol(p usagetap.Protocol) bool {
	switch p {
	case usagetap.Responses, usagetap.ChatCompletions, usagetap.Messages, usagetap.Bedrock, usagetap.Gemini, usagetap.Ollama:
		return true
	default:
		return false
	}
}

func accumulateTimelineRecord(a *timelineAggregate, rec MoneyRecord, currencies map[string]bool) {
	accumulateTimelineCost(a, rec.Money, currencies)
	if rec.Usage == nil || rec.Usage.Usage.Source != usagetap.Reported || !supportedTimelineProtocol(rec.Usage.Protocol) {
		a.unknownTokens++
		return
	}
	mi := moneyInputFromUsage(MoneyInput{}, *rec.Usage)
	values := []int64{mi.InputTokens, mi.OutputTokens, mi.CacheReadTokens, mi.CacheWriteTokens}
	total := int64(0)
	for _, value := range values {
		if value < 0 || total > maxMoneyMicros-value {
			a.unknownTokens++
			return
		}
		total += value
	}
	if a.tokens > maxMoneyMicros-total {
		a.unknownTokens++
		return
	}
	// Count the same evidence in the total and the breakdown, or in neither.
	a.tokens += total
	a.tokenParts.Input += mi.InputTokens
	a.tokenParts.Output += mi.OutputTokens
	a.tokenParts.CacheRead += mi.CacheReadTokens
	a.tokenParts.CacheWrite += mi.CacheWriteTokens
	a.tokenKnown = true
}

func accumulateTimelineCost(a *timelineAggregate, m Money, currencies map[string]bool) {
	if m.Billing == "plan" {
		a.plan++
		// As in the summary, a request known not to have been dispatched
		// does not reduce estimate coverage.
		if m.Kind != "no_charge_expected" {
			accumulateTimelinePlanCost(a, m.APIEquivalent, currencies)
		}
		return
	}
	if m.Billing == "pay_as_you_go" && m.Amount != nil && *m.Amount >= 0 && *m.Amount <= maxMoneyMicros && validMoneyMetadata(m.Currency, m.Source, m.AsOf) && (m.Kind == "charged" || m.Kind == "estimated" || m.Kind == "estimated_floor") {
		if *m.Amount > maxMoneyMicros-a.cost[m.Currency] {
			a.unknownCost++
			a.estimated[m.Currency] = true
		} else {
			a.cost[m.Currency] += *m.Amount
			currencies[m.Currency] = true
			if m.Kind != "charged" {
				a.estimated[m.Currency] = true
			}
			if m.Kind == "estimated_floor" {
				a.unknownCost++
			}
		}
	} else if m.Billing != "local" && m.Kind != "no_charge_expected" {
		a.unknownCost++
	}
}

func accumulateTimelinePlanCost(a *timelineAggregate, e *APIEquivalent, currencies map[string]bool) {
	// Mirror addAPIEquivalent's frozen evidence checks: no repricing,
	// and a partial estimate must establish a positive lower bound.
	if e == nil || (e.Kind != "estimated" && e.Kind != "estimated_floor") || e.Amount == nil ||
		*e.Amount < 0 || *e.Amount > maxMoneyMicros || !validMoneyMetadata(e.Currency, e.Source, e.AsOf) ||
		(e.Kind == "estimated_floor" && *e.Amount == 0) {
		a.unknownCost++
		return
	}
	current := a.planCost[e.Currency]
	overflow := *e.Amount > maxMoneyMicros-current.micros
	if e.Kind == "estimated_floor" || overflow {
		a.unknownCost++
	}
	if overflow {
		return
	}
	if a.planCost == nil {
		a.planCost = make(map[string]timelinePlanCost)
	}
	current.micros += *e.Amount
	if e.Kind == "estimated" {
		current.complete++
	}
	a.planCost[e.Currency] = current
	currencies[e.Currency] = true
}
