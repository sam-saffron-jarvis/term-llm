package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupOfflinePricingExactDatedSubset(t *testing.T) {
	tests := []struct {
		provider, model, asof string
		input, read, output   int64
		threshold             int64
	}{
		{"openai", "gpt-5.5", "2026-09-15", 5000000, 500000, 30000000, 272000},
		{"openai", "gpt-5.6-terra", "2026-09-15", 2000000, 200000, 12000000, 272000},
		{"openai", "gpt-5.3-codex", "2026-09-15", 1750000, 175000, 14000000, 272000},
		{"anthropic", "claude-opus-5", "2026-09-14", 5000000, 500000, 25000000, 0},
		{"anthropic", "claude-opus-4-8", "2026-09-14", 5000000, 500000, 25000000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := LookupOfflinePricing(tt.provider, tt.model)
			if !ok || got.Currency != "USD" || got.AsOf != tt.asof || got.Source == "" || got.Base.Input != tt.input || got.Base.CacheRead != tt.read || got.Base.Output != tt.output || got.LongContextThreshold != tt.threshold {
				t.Fatalf("dated prices: %+v %v", got, ok)
			}
			canonical := bundledPricing[tt.model]
			expected, valid := offlineTokenPrices(canonical.InputCostPerToken, canonical.OutputCostPerToken, canonical.CacheReadInputTokenCost, canonical.CacheCreationInputTokenCost)
			if !valid || got.Base != expected {
				t.Fatal("adapter drifted from canonical numerical catalog", got)
			}
			if tt.threshold > 0 {
				if got.LongContext == nil || got.LongContext.Input != tt.input*2 || got.LongContext.CacheRead != tt.read*2 || got.LongContext.Output != tt.output*3/2 {
					t.Fatal(got)
				}
			} else if got.LongContext != nil || got.CacheWriteTTL != "5m" || got.Base.CacheWrite != 6250000 {
				t.Fatal(got)
			}
		})
	}
}
func TestLookupOfflinePricingDoesNotGuessAliasesProvidersOrDates(t *testing.T) {
	for _, tt := range [][2]string{
		{"openai", "gpt-5.5-high"}, {"openai", "openai/gpt-5.5"}, {"openai", "gpt-5.5-2026-09-15"},
		{"anthropic", "opus"}, {"anthropic", "claude-opus-5-20260914"}, {"anthropic", "claude-opus-4-7"},
		{"anthropic", "claude-sonnet-5"}, {"openrouter", "gpt-5.5"}, {"custom", "gpt-5.6-terra"}, {"OpenAI", "gpt-5.5"},
		{"openai", "claude-opus-5"}, {"anthropic", "gpt-5.5"}, {"openai", "unknown"},
		// Promotional entries are deliberately outside this small dated subset.
		{"openai", "gpt-5.6-sol"},
	} {
		if p, ok := LookupOfflinePricing(tt[0], tt[1]); ok || p.Source != "" || p.AsOf != "" {
			t.Fatalf("invented evidence for %q: %+v", tt, p)
		}
	}
}
func TestLookupOfflinePricingIsPureAndReturnsIndependentValues(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "must-not-create")
	t.Setenv("HOME", absent)
	t.Setenv("XDG_CACHE_HOME", absent)
	p, ok := LookupOfflinePricing("openai", "gpt-5.6-terra")
	if !ok {
		t.Fatal("offline lookup failed")
	}
	p.Base.Input = 1
	p.LongContext.Input = 2
	again, ok := LookupOfflinePricing("openai", "gpt-5.6-terra")
	if !ok || again.Base.Input != 2000000 || again.LongContext.Input != 4000000 {
		t.Fatal("caller mutated catalog", again)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("offline metadata lookup touched HOME/cache", err)
	}
}
func TestOfflinePriceConversionDoesNotSilentlyRoundUnsupportedPrecision(t *testing.T) {
	if _, ok := offlineTokenPrices(0.1e-12, 1, 1, 1); ok {
		t.Fatal("sub-micro price was guessed")
	}
	if _, ok := offlineTokenPrices(-1, 1, 1, 1); ok {
		t.Fatal("negative price accepted")
	}
}
