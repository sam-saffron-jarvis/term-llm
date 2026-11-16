package usage

import "math"

// OfflineTokenPrices are integer currency micros per million tokens, converted
// from the canonical bundled catalog. They are rates, not a request charge.
type OfflineTokenPrices struct {
	Input, Output, CacheRead, CacheWrite int64
}

// OfflinePricing contains only explicitly dated first-party list-price evidence.
// LongContext applies to the WHOLE request when all input (including caches)
// exceeds LongContextThreshold. CacheWriteTTL names the write price's required
// TTL, not evidence that a particular request used that TTL.
type OfflinePricing struct {
	Currency, Source, AsOf string
	Base                   OfflineTokenPrices
	LongContext            *OfflineTokenPrices
	LongContextThreshold   int64
	CacheWriteTTL          string
}

// LookupOfflinePricing reads the bundled map directly: no fetcher, filesystem,
// network, prefix/substring matching, effort aliases or provider-name inference.
// The caller must independently establish the first-party upstream and exact
// service tier. Entries without a recorded verification date are not exposed.
func LookupOfflinePricing(provider, model string) (OfflinePricing, bool) {
	var result OfflinePricing
	switch provider {
	case "openai":
		switch model {
		case "gpt-5.5", "gpt-5.6-terra", "gpt-5.3-codex":
		default:
			return result, false
		}
		result.Source = "Bundled OpenAI list pricing (developers.openai.com)"
		result.AsOf = "2026-09-15"
	case "anthropic":
		switch model {
		case "claude-opus-5", "claude-opus-4-8":
		default:
			return result, false
		}
		result.Source = "Bundled Anthropic list pricing"
		result.AsOf = "2026-09-14"
		result.CacheWriteTTL = "5m"
	default:
		return result, false
	}
	p, ok := bundledPricing[model]
	if !ok {
		return OfflinePricing{}, false
	}
	result.Currency = "USD"
	result.Base, ok = offlineTokenPrices(p.InputCostPerToken, p.OutputCostPerToken, p.CacheReadInputTokenCost, p.CacheCreationInputTokenCost)
	if !ok {
		return OfflinePricing{}, false
	}
	if p.WholeRequestTier {
		if p.TieredThreshold <= 0 {
			return OfflinePricing{}, false
		}
		tier, valid := offlineTokenPrices(p.InputCostPerTokenAbove200k, p.OutputCostPerTokenAbove200k, p.CacheReadCostAbove200k, p.CacheCreationCostAbove200k)
		if !valid {
			return OfflinePricing{}, false
		}
		result.LongContext = &tier
		result.LongContextThreshold = int64(p.TieredThreshold)
	}
	return result, true
}
func offlineTokenPrices(input, output, read, write float64) (OfflineTokenPrices, bool) {
	values := []float64{input, output, read, write}
	var micros [4]int64
	for i, v := range values {
		// USD/token -> USD micros/million tokens. Only exact integer-micro rates
		// are admitted; tolerance accommodates representation of the catalog floats.
		scaled := v * 1_000_000_000_000
		rounded := math.Round(scaled)
		if math.IsNaN(scaled) || math.IsInf(scaled, 0) || scaled < 0 || rounded > 9_007_199_254_740_991 || math.Abs(scaled-rounded) > 0.000001 {
			return OfflineTokenPrices{}, false
		}
		micros[i] = int64(rounded)
	}
	return OfflineTokenPrices{Input: micros[0], Output: micros[1], CacheRead: micros[2], CacheWrite: micros[3]}, true
}
