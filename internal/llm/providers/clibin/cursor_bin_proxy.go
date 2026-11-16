package clibin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/providerspec"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

// Cursor resolves three independent origins. Each must be an explicitly
// registered sibling route; the agent origin is otherwise server-advertised.
const (
	cursorProxyAgentSuffix = "-agent"
	cursorProxyRepoSuffix  = "-repo"
)

// cursorProxyRootFlags are the options that select Cursor's origins and
// transports. A defaulted --endpoint outranks CURSOR_API_ENDPOINT, so the
// environment alone cannot pin routing.
var cursorProxyRootFlags = map[string]bool{
	"-e": true, "--endpoint": true, "--agent-endpoint": true, "--repo-endpoint": true,
	"--http-version": true, "--repo-http-version": true, "--auth-token": true, "--api-key": true,
}

// cursorProxyEgressSink is an HTTP proxy address nothing listens on.
// cursor-agent 2026.02.27 contacts a hardcoded https://api2.cursor.sh (Statsig
// setup) whatever roots it is given, and that request can carry its stored
// token. Node's environment-proxy mode sends every host except the scoped
// roots and loopback to this sink instead. It is a best-effort application
// control, not an egress sandbox, and it means the scoped roots cannot
// themselves be reached through a corporate HTTP proxy.
const cursorProxyEgressSink = "http://127.0.0.1:9"

func cursorProxyNoProxy(roots ...string) string {
	hosts := []string{"127.0.0.1", "localhost", "::1"}
	for _, root := range roots {
		if u, err := url.Parse(root); err == nil && u.Hostname() != "" && !slices.Contains(hosts, u.Hostname()) {
			hosts = append(hosts, u.Hostname())
		}
	}
	return strings.Join(hosts, ",")
}

func cursorProxyRoots(binding *cliProxyBinding) (agent, repo string, err error) {
	agent, okAgent := siblingCLIProxyRoot(binding, cursorProxyAgentSuffix)
	repo, okRepo := siblingCLIProxyRoot(binding, cursorProxyRepoSuffix)
	if !okAgent || !okRepo {
		return "", "", fmt.Errorf("cursor-bin proxy mode requires scoped backend, agent and repository roots")
	}
	return agent, repo, nil
}

// cursorProxyArgs prepends the forced origins for a proxy-bound provider and
// removes any other occurrence. The token is never placed in argv. With an
// incomplete binding the arguments are unchanged and the launch fails closed
// when its environment is prepared.
func (p *CursorBinProvider) cursorProxyArgs(args []string) []string {
	if !cliProxyRequested(p.extraEnv, cursorProxyRootEnv) {
		return args
	}
	root := p.effectiveEnv(cursorProxyRootEnv)
	found, ok := proxyclient.Lookup(root)
	if !ok || !proxyclient.SameRoot(found.Root, root) {
		return args
	}
	binding := &cliProxyBinding{Root: found.Root, Token: found.Token, Kind: found.Kind}
	agent, repo, err := cursorProxyRoots(binding)
	if err != nil {
		return args
	}
	out := []string{
		"--endpoint", binding.Root, "--agent-endpoint", agent, "--repo-endpoint", repo,
		"--http-version", "1.1", "--repo-http-version", "1.1",
	}
	for i := 0; i < len(args); i++ {
		name, _, inline := strings.Cut(args[i], "=")
		if cursorProxyRootFlags[name] {
			if !inline {
				i++
			}
			continue
		}
		if args[i] == "-k" || args[i] == "--insecure" || args[i] == "--repo-insecure" {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// cursorProxySecrets lists the scoped token for diagnostic redaction.
func (p *CursorBinProvider) cursorProxySecrets() []string {
	if !cliProxyRequested(p.extraEnv, cursorProxyRootEnv) {
		return nil
	}
	if binding, ok := proxyclient.Lookup(p.effectiveEnv(cursorProxyRootEnv)); ok {
		return []string{binding.Token}
	}
	return nil
}

// cursorCredentialStore keeps a proxy-bound provider from importing the real
// Cursor login into its private home.
func (p *CursorBinProvider) cursorCredentialStore() string {
	if cliProxyRequested(p.extraEnv, cursorProxyRootEnv) {
		return "memory"
	}
	return strings.ToLower(p.effectiveEnv("AGENT_CLI_CREDENTIAL_STORE"))
}

// prepareCursorProxyEnvironment gives cursor-agent an opaque scoped token and
// a private config and credential namespace. cursor-agent persists the token
// it is given, so its credential file must never be the user's: on Linux that
// file is $XDG_CONFIG_HOME/<name>/auth.json, not CURSOR_CONFIG_DIR.
func prepareCursorProxyEnvironment(env []string) ([]string, error) {
	binding, err := resolveCLIProxyBinding(env, cursorProxyRootEnv, providerspec.TypeCursorBin, "cursor-bin")
	if binding == nil || err != nil {
		return env, err
	}
	if runtime.GOOS != "linux" {
		// macOS and Windows credential stores are not scoped by these roots;
		// the scoped token would be written over the user's real Cursor login.
		return nil, fmt.Errorf("cursor-bin proxy mode is only isolated on Linux; refusing to touch the %s Cursor credential store", runtime.GOOS)
	}
	agent, repo, err := cursorProxyRoots(binding)
	if err != nil {
		return nil, err
	}
	home := strings.TrimSpace(envValue(env, "HOME"))
	if real, _ := os.UserHomeDir(); home == "" || !filepath.IsAbs(home) || filepath.Clean(home) == filepath.Clean(real) {
		return nil, fmt.Errorf("cursor-bin proxy mode requires the term-llm owned private home")
	}
	configDir := filepath.Join(binding.Dir, "config")
	xdgDir := filepath.Join(binding.Dir, "xdg")
	for _, dir := range []string{configDir, xdgDir} {
		if err := ensurePrivateCLIProxyDir(dir); err != nil {
			return nil, fmt.Errorf("cursor-bin proxy state: %w", err)
		}
	}
	// Rewritten for every launch: the agent stream only uses an ordinary
	// HTTP/1.1 route when this switch is set; --http-version does not control it.
	config, err := json.Marshal(map[string]any{
		"version": 1, "editor": map[string]bool{"vimMode": false}, "hasChangedDefaultModel": false,
		"permissions": map[string][]string{"allow": {}, "deny": {}},
		"network":     map[string]bool{"useHttp1ForAgent": true},
	})
	if err != nil {
		return nil, err
	}
	if err := fileprivacy.WriteFileAtomic(filepath.Join(configDir, "cli-config.json"), config); err != nil {
		return nil, fmt.Errorf("write cursor-bin proxy config: %w", err)
	}
	// The token cursor-agent persisted on an earlier launch must not be
	// available to startup code that runs before the forced roots apply.
	if err := os.Remove(filepath.Join(xdgDir, "cursor", "auth.json")); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("clear cursor-bin proxy credential file: %w", err)
	}
	forced := map[string]string{
		"CURSOR_AUTH_TOKEN":   binding.Token,
		"CURSOR_API_ENDPOINT": binding.Root,
		"CURSOR_API_BASE_URL": binding.Root,
		"CURSOR_CONFIG_DIR":   configDir,
		"XDG_CONFIG_HOME":     xdgDir,
		"XDG_CACHE_HOME":      filepath.Join(binding.Dir, "cache"),
		"NODE_USE_ENV_PROXY":  "1",
		"NO_PROXY":            cursorProxyNoProxy(binding.Root, agent, repo),
	}
	forced["no_proxy"] = forced["NO_PROXY"]
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		forced[key] = cursorProxyEgressSink
	}
	// CURSOR_DATA_DIR stays the provider's owned session directory. Every other
	// CURSOR_* or credential-store value, including an API key, is dropped.
	return forceCLIProxyEnvironment(env, forced, func(key string) bool {
		if key == "CURSOR_DATA_DIR" {
			return true
		}
		return !hasAnyPrefix(key, "CURSOR_", "AGENT_CLI_") && key != cursorProxyRootEnv
	}), nil
}
