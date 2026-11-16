package clibin

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/term-llm/term-llm/internal/providerspec"
)

const (
	vibeProxyProviderName = "term-llm-proxy"
	vibeProxyTokenEnv     = "TERM_LLM_VIBE_PROXY_TOKEN"
)

// vibeProxyModelAlias is the alias the scoped model is registered under.
// vibe-acp 3.1.2 merges VIBE_MODELS with its built-in models, so the alias is
// kept distinct from every built-in entry.
func vibeProxyModelAlias(model string) string { return vibeProxyProviderName + "." + model }

// vibeSelectedModel is the model alias Vibe must report as active.
func (p *VibeBinProvider) vibeSelectedModel(model string) string {
	if model == "" || !cliProxyRequested(p.extraEnv, vibeProxyRootEnv) {
		return model
	}
	return vibeProxyModelAlias(model)
}

// prepareVibeProxyEnvironment selects one generic OpenAI-style Vibe provider
// whose api_base is the scoped route and whose key comes from a dedicated
// variable, following the mistral-vibe 3.1.2 configuration schema.
func prepareVibeProxyEnvironment(env []string) ([]string, error) {
	binding, err := resolveCLIProxyBinding(env, vibeProxyRootEnv, providerspec.TypeVibeBin, "vibe-bin")
	if binding == nil || err != nil {
		return env, err
	}
	model := strings.TrimSpace(envValue(env, "VIBE_ACTIVE_MODEL"))
	if model == "" {
		return nil, fmt.Errorf("vibe-bin proxy mode requires an explicit model")
	}
	home := filepath.Join(binding.Dir, "vibe-home")
	if err := ensurePrivateCLIProxyDir(home); err != nil {
		return nil, fmt.Errorf("vibe-bin proxy state: %w", err)
	}
	// A distinct provider name avoids Vibe's first-party "mistral" sign-in
	// semantics; a distinct alias cannot collide with a built-in model entry.
	alias := vibeProxyModelAlias(model)
	providers, err := json.Marshal([]map[string]string{{
		"name": vibeProxyProviderName, "api_base": binding.Root, "api_key_env_var": vibeProxyTokenEnv,
		"api_style": "openai", "backend": "generic",
	}})
	if err != nil {
		return nil, err
	}
	models, err := json.Marshal([]map[string]string{{"name": model, "provider": vibeProxyProviderName, "alias": alias}})
	if err != nil {
		return nil, err
	}
	forced := map[string]string{
		"VIBE_HOME":         home,
		"VIBE_ACTIVE_MODEL": alias,
		"VIBE_PROVIDERS":    string(providers),
		"VIBE_MODELS":       string(models),
		vibeProxyTokenEnv:   binding.Token,
		// vibe-acp 3.1.2 otherwise reports errors straight to a Sentry host.
		"VIBE_ENABLE_TELEMETRY": "false",
	}
	for key, value := range vibeForcedEnv {
		forced[key] = value
	}
	// Any other VIBE_* layer value could re-select a provider, model or OIDC
	// sign-in, and a Mistral key would outrank the scoped route's own key.
	return forceCLIProxyEnvironment(env, forced, func(key string) bool {
		return !hasAnyPrefix(key, "VIBE_", "MISTRAL_") && key != vibeProxyRootEnv
	}), nil
}
