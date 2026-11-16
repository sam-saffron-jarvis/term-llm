package clibin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/providerspec"
	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

// Provider Env entries that switch a CLI adapter from the local vendor login
// to an explicit scoped proxy root. Configuration sets them; nothing else does.
const (
	cursorProxyRootEnv = "TERM_LLM_CURSOR_PROXY_ROOT"
	grokProxyRootEnv   = "TERM_LLM_GROK_CLI_PROXY_ROOT"
	vibeProxyRootEnv   = "TERM_LLM_VIBE_PROXY_ROOT"
	agyProxyRootEnv    = "TERM_LLM_AGY_PROXY_ROOT"
)

// cliProxyBinding is one resolved scoped route and its private state directory.
type cliProxyBinding struct{ Root, Token, Kind, Dir string }

// prepareCLIProxyEnvironment is applied to the final child environment of
// every CLI launch, after provider and inherited values were merged. A marker
// without a matching registered binding fails before exec. This is routing and
// credential-namespace isolation, not a network sandbox.
func prepareCLIProxyEnvironment(provider string, env []string) ([]string, error) {
	switch {
	case strings.HasPrefix(provider, "claude-bin"):
		return prepareClaudeProxyEnvironment(env)
	case strings.HasPrefix(provider, "cursor-bin"):
		return prepareCursorProxyEnvironment(env)
	case strings.HasPrefix(provider, "grok-bin"):
		return prepareGrokProxyEnvironment(env)
	case strings.HasPrefix(provider, "vibe-bin"):
		return prepareVibeProxyEnvironment(env)
	case strings.HasPrefix(provider, "agy-bin"):
		return env, rejectAgyProxyEnvironment(env)
	}
	return env, nil
}

// rejectAgyProxyEnvironment fails closed: no credentialless Antigravity adapter
// exists, so a proxy-bound agy-bin must never fall back to the local login.
func rejectAgyProxyEnvironment(env []string) error {
	if envValue(env, agyProxyRootEnv) == "" {
		return nil
	}
	return fmt.Errorf("agy-bin has no credentialless proxy adapter (it requires agy %s or newer verified in isolation); refusing to use the local Antigravity login", agyBinMinimumVersion)
}

// cliProxyRequested reports whether a provider was bound to a proxy root, for
// decisions that precede child-environment construction.
func cliProxyRequested(extraEnv map[string]string, marker string) bool {
	if value, ok := extraEnv[marker]; ok {
		return strings.TrimSpace(value) != ""
	}
	return strings.TrimSpace(os.Getenv(marker)) != ""
}

// resolveCLIProxyBinding returns nil when the marker is absent. Otherwise the
// root must be an exactly registered binding of the profile's credential kind.
func resolveCLIProxyBinding(env []string, marker string, kind providerspec.Type, name string) (*cliProxyBinding, error) {
	root := envValue(env, marker)
	if root == "" {
		return nil, nil
	}
	profile, ok := proxyprofile.ForProvider(kind)
	if !ok {
		return nil, fmt.Errorf("%s has no proxy profile", name)
	}
	binding, ok := proxyclient.Lookup(root)
	if !ok || binding.Kind != profile.Account || !proxyclient.SameRoot(binding.Root, root) {
		return nil, fmt.Errorf("%s proxy root has no scoped credential binding", name)
	}
	dir, err := cliProxyStateDir(name, binding.Root, binding.Token)
	if err != nil {
		return nil, err
	}
	return &cliProxyBinding{Root: binding.Root, Token: binding.Token, Kind: binding.Kind, Dir: dir}, nil
}

// siblingCLIProxyRoot returns an explicitly registered sibling route of the
// same proxy, principal and credential kind. It never invents a destination.
func siblingCLIProxyRoot(binding *cliProxyBinding, suffix string) (string, bool) {
	root := binding.Root + suffix
	sibling, ok := proxyclient.Lookup(root)
	if !ok || sibling.Token != binding.Token || sibling.Kind != binding.Kind || !proxyclient.SameRoot(sibling.Root, root) {
		return "", false
	}
	return sibling.Root, true
}

// cliProxyStateDir is the private per-binding namespace for a CLI's config and
// credential files. No real user login or configuration is imported into it.
func cliProxyStateDir(name, root, token string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate private %s proxy state: %w", name, err)
	}
	sum := sha256.Sum256([]byte(root + "\x00" + token))
	dir := filepath.Join(cache, "term-llm", name+"-proxy", hex.EncodeToString(sum[:16]))
	if err := ensurePrivateCLIProxyDir(dir); err != nil {
		return "", fmt.Errorf("%s proxy state: %w", name, err)
	}
	return dir, nil
}

func ensurePrivateCLIProxyDir(dir string) error {
	if err := fileprivacy.MkdirAll(dir); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || fileprivacy.CheckDir(st) != nil {
		return fmt.Errorf("%s must be a private directory", dir)
	}
	return nil
}

// forceCLIProxyEnvironment drops every entry keep rejects, then appends the
// forced values, so no inherited or configured entry can outrank them.
func forceCLIProxyEnvironment(env []string, forced map[string]string, keep func(key string) bool) []string {
	out := make([]string, 0, len(env)+len(forced))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := forced[key]; replace || !keep(key) {
			continue
		}
		out = append(out, entry)
	}
	for key, value := range forced {
		out = append(out, key+"="+value)
	}
	return out
}

func hasAnyPrefix(key string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
