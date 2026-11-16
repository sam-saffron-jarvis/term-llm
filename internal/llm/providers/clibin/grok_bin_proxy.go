package clibin

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/term-llm/term-llm/internal/acp"
	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/providerspec"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

const (
	grokProxyConfigMarker = "# term-llm scoped proxy routing (managed; do not edit)"
	grokACPAPIKeyMethod   = "xai.api_key"
)

// grokProxyEndpointEnv pins every documented Grok CLI service root that has an
// environment override. Model inference, the model catalog and the session
// service all resolve below the one scoped route; feedback, managed-config and
// trace destinations are pinned there too so they cannot become direct egress.
var grokProxyEndpointEnv = map[string]string{
	"GROK_CLI_CHAT_PROXY_BASE_URL": "",
	"GROK_MODELS_BASE_URL":         "",
	"GROK_MODELS_LIST_URL":         "/models",
	"GROK_XAI_API_BASE_URL":        "",
	"GROK_FEEDBACK_BASE_URL":       "/feedback",
	"GROK_MANAGED_CONFIG_URL":      "/managed-config",
	"GROK_TRACE_UPLOAD_URL":        "/traces",
}

// renderGrokProxyConfig is appended to the owned config.toml. Environment
// values already pin the roots; this also disables telemetry, feedback and
// managed configuration, and pins API-key auth so the CLI never falls through
// to an OIDC or cached grok.com session.
func renderGrokProxyConfig(root string) string {
	var b strings.Builder
	b.WriteString("\n" + grokProxyConfigMarker + "\n")
	b.WriteString("[features]\ntelemetry = false\nfeedback = false\nmanaged_config = false\n\n")
	b.WriteString("[managed_mcps]\nenabled = false\n\n")
	b.WriteString("[auth]\npreferred_method = \"api_key\"\n\n")
	b.WriteString("[endpoints]\n")
	for _, key := range []string{"cli_chat_proxy_base_url", "models_base_url", "xai_api_base_url"} {
		fmt.Fprintf(&b, "%s = %s\n", key, strconv.Quote(root))
	}
	fmt.Fprintf(&b, "models_list_url = %s\n", strconv.Quote(root+"/models"))
	fmt.Fprintf(&b, "feedback_base_url = %s\n", strconv.Quote(root+"/feedback"))
	fmt.Fprintf(&b, "managed_config_url = %s\n", strconv.Quote(root+"/managed-config"))
	fmt.Fprintf(&b, "trace_upload_url = %s\n", strconv.Quote(root+"/traces"))
	return b.String()
}

// grokProxyConfig is the fragment for a proxy-bound provider, or "" otherwise.
// An unregistered root yields no fragment; the launch itself then fails closed.
func (p *GrokBinProvider) grokProxyConfig() string {
	if !cliProxyRequested(p.extraEnv, grokProxyRootEnv) {
		return ""
	}
	root := p.extraEnv[grokProxyRootEnv]
	if root == "" {
		root = os.Getenv(grokProxyRootEnv)
	}
	binding, ok := proxyclient.Lookup(root)
	if !ok || !proxyclient.SameRoot(binding.Root, root) {
		return ""
	}
	return renderGrokProxyConfig(binding.Root)
}

// selectGrokACPAuthMethod never offers the cached grok.com session to a
// proxy-bound provider: only the scoped API-key method is acceptable there.
func (p *GrokBinProvider) selectGrokACPAuthMethod(methods []acp.AuthMethod) string {
	if !cliProxyRequested(p.extraEnv, grokProxyRootEnv) {
		return p.selectGrokACPAuth(methods)
	}
	for _, method := range methods {
		if method.ID == grokACPAPIKeyMethod {
			return grokACPAPIKeyMethod
		}
	}
	return ""
}

// prepareGrokProxyEnvironment forces the Grok CLI onto one scoped route. The
// scoped token travels as XAI_API_KEY, which is safe only because every xAI
// service root is pinned to the same route in the same step.
func prepareGrokProxyEnvironment(env []string) ([]string, error) {
	binding, err := resolveCLIProxyBinding(env, grokProxyRootEnv, providerspec.TypeGrokBin, "grok-bin")
	if binding == nil || err != nil {
		return env, err
	}
	grokHome := strings.TrimSpace(envValue(env, "GROK_HOME"))
	if grokHome == "" || !filepath.IsAbs(grokHome) {
		return nil, fmt.Errorf("grok-bin proxy mode requires the term-llm owned GROK_HOME")
	}
	if real := GrokBinAuthPath(""); real != "" && filepath.Clean(grokHome) == filepath.Dir(real) {
		return nil, fmt.Errorf("grok-bin proxy mode refuses the user's own Grok home")
	}
	if err := ensureGrokProxyConfig(grokHome, binding.Root); err != nil {
		return nil, err
	}
	home := filepath.Join(binding.Dir, "home")
	if err := ensurePrivateCLIProxyDir(home); err != nil {
		return nil, fmt.Errorf("grok-bin proxy state: %w", err)
	}
	forced := map[string]string{
		"XAI_API_KEY":               binding.Token,
		"GROK_HOME":                 grokHome,
		"GROK_AUTH_PATH":            filepath.Join(binding.Dir, "auth.json"),
		"GROK_DISABLE_AUTOUPDATER":  "1",
		"GROK_DISABLE_EXPERIMENTS":  "1",
		"GROK_FEEDBACK_ENABLED":     "false",
		"GROK_MANAGED_MCPS_ENABLED": "false",
		"HOME":                      home,
		"USERPROFILE":               home,
		"XDG_CONFIG_HOME":           filepath.Join(home, ".config"),
		"XDG_DATA_HOME":             filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":            filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":            filepath.Join(home, ".cache"),
	}
	for key, suffix := range grokProxyEndpointEnv {
		forced[key] = binding.Root + suffix
	}
	// Every other inherited or configured GROK_*/XAI_* value is dropped: config
	// overlays, auth helpers, deployment keys and OIDC settings can all re-route.
	return forceCLIProxyEnvironment(env, forced, func(key string) bool {
		return !hasAnyPrefix(key, "GROK_", "XAI_") && key != grokProxyRootEnv
	}), nil
}

// ensureGrokProxyConfig guarantees the owned config.toml ends with the routing
// fragment for root, including homes that never had a config written.
func ensureGrokProxyConfig(grokHome, root string) error {
	path := filepath.Join(grokHome, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read grok-bin config: %w", err)
	}
	fragment := renderGrokProxyConfig(root)
	current := string(data)
	if strings.HasSuffix(current, fragment) && strings.Count(current, grokProxyConfigMarker) == 1 {
		return nil
	}
	if i := strings.Index(current, "\n"+grokProxyConfigMarker); i >= 0 {
		current = current[:i]
	}
	if err := fileprivacy.WriteFileAtomic(path, []byte(current+fragment)); err != nil {
		return fmt.Errorf("write grok-bin proxy config: %w", err)
	}
	return nil
}
