package proxyprofile

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/providerspec"
)

func TestEveryBuiltinProviderHasProxyDecision(t *testing.T) {
	for _, spec := range providerspec.All() {
		if _, ok := ForProvider(spec.Type); !ok {
			t.Errorf("missing proxy decision for %s", spec.Type)
		}
	}
	if _, ok := ForProvider(providerspec.TypeOpenAICompat); !ok {
		t.Fatal("missing custom compatible provider decision")
	}
}
func TestProfileIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range All() {
		if p.ID == "" || seen[p.ID] {
			t.Fatalf("duplicate/empty profile %q", p.ID)
		}
		seen[p.ID] = true
	}
}

// api.openai.com embeddings and realtime reached with a ChatGPT OAuth token have
// no evidence of subscription inclusion, so they must not be presented as plan usage.
func TestChatGPTOpenAIAncillaryBillingIsNotClaimedAsPlan(t *testing.T) {
	for _, p := range All() {
		if p.ID != "chatgpt-openai" {
			continue
		}
		if p.Billing != "unspecified" || strings.Contains(strings.ToLower(p.Label), "plan") || !strings.Contains(p.Label, "billing unspecified") {
			t.Fatalf("billing=%q label=%q", p.Billing, p.Label)
		}
		return
	}
	t.Fatal("chatgpt-openai profile missing")
}

// A CLI provider reaches the proxy only through its adapter marker. Grok CLI is
// ungated on real-executable fixture evidence and shares the native Grok plan
// account; Cursor, Antigravity and Vibe stay gated.
func TestCLIProfilesNameTheirAdapterMarkerAndStayGated(t *testing.T) {
	gated := map[providerspec.Type]bool{providerspec.TypeCursorBin: true, providerspec.TypeAgyBin: true, providerspec.TypeVibeBin: true}
	for _, kind := range []providerspec.Type{providerspec.TypeClaudeBin, providerspec.TypeCursorBin, providerspec.TypeGrokBin, providerspec.TypeAgyBin, providerspec.TypeVibeBin} {
		p, ok := ForProvider(kind)
		if !ok || !strings.HasPrefix(p.CLIRootEnv, "TERM_LLM_") || !strings.HasSuffix(p.CLIRootEnv, "_PROXY_ROOT") {
			t.Errorf("%s has no adapter marker: %q", kind, p.CLIRootEnv)
		}
		if gated[kind] != (p.Gate != "") {
			t.Errorf("%s gate = %q", kind, p.Gate)
		}
	}
	if grok, _ := ForProvider(providerspec.TypeGrokBin); grok.Account != "grok" || grok.Upstream == "" {
		t.Errorf("grok-bin must bind the Grok plan account kind: %+v", grok)
	}
	for _, p := range All() {
		if strings.HasPrefix(p.ID, "cursor-bin-") && p.Gate == "" {
			t.Errorf("%s must stay gated with cursor-bin", p.ID)
		}
		if p.CLIRootEnv != "" && p.Provider == "" {
			t.Errorf("%s names an adapter marker without a provider", p.ID)
		}
	}
}
