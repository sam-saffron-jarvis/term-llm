package proxy

import (
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func timelineBucketIndex(t time.Time, bounds []time.Time) int {
	for i := 0; i < len(bounds)-1; i++ {
		if !t.Before(bounds[i]) && (t.Before(bounds[i+1]) || (i == len(bounds)-2 && t.Equal(bounds[i+1]))) {
			return i
		}
	}
	return -1
}

func timelineGroupIdentity(rec MoneyRecord, caller, route, group string) (string, string) {
	if group == "client" {
		key := rec.DeviceID
		if key == "" {
			key = caller
		}
		label := rec.Device
		if label == "" {
			label = "Unknown client"
		}
		return key, label
	}
	if group == "provider" {
		key := rec.Provider
		if key == "" {
			key = route
		}
		label := timelineProviderLabel(key)
		if label == "" {
			label = key
		}
		if label == "" {
			label = "Unknown provider"
		}
		return key, label
	}
	return "total", "Total"
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
	a.requests++
	m := rec.Money
	if m.Billing == "plan" {
		a.plan++
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
	} else if m.Billing != "plan" && m.Billing != "local" && m.Kind != "no_charge_expected" {
		a.unknownCost++
	}
	if rec.Usage == nil || rec.Usage.Usage.Source != "reported" || !supportedTimelineProtocol(rec.Usage.Protocol) {
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
	a.tokens += total
	a.tokenKnown = true
}
