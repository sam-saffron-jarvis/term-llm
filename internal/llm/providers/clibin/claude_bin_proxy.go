package clibin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/term-llm/term-llm/internal/fileprivacy"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

const claudeProxyRootEnv = "TERM_LLM_CLAUDE_PROXY_ROOT"

// prepareClaudeProxyEnvironment runs after normal child-environment construction
// for inference and model listing. This is routing/auth isolation, not a network
// sandbox: credential custody still requires a separate worker filesystem/user.
func prepareClaudeProxyEnvironment(env []string) ([]string, error) {
	root := envValue(env, claudeProxyRootEnv)
	if root == "" {
		return env, nil
	}
	binding, ok := proxyclient.Lookup(root)
	if !ok || binding.Kind != "claude-code" || !proxyclient.SameRoot(binding.Root, root) {
		return nil, fmt.Errorf("claude-bin proxy root has no scoped credential binding")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate private Claude proxy state: %w", err)
	}
	sum := sha256.Sum256([]byte(binding.Root + "\x00" + binding.Token))
	dir := filepath.Join(cache, "term-llm", "claude-proxy", hex.EncodeToString(sum[:16]))
	if err := fileprivacy.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("create private Claude proxy state: %w", err)
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || fileprivacy.CheckDir(st) != nil {
		return nil, fmt.Errorf("Claude proxy state must be a private directory")
	}
	// Only onboarding state is born here. No login, API-key helper, hooks or real
	// user's Claude configuration is imported. Native session files may persist.
	path := filepath.Join(dir, ".claude.json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		err = json.NewEncoder(f).Encode(map[string]bool{"hasCompletedOnboarding": true})
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	} else if os.IsExist(err) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("initialize Claude proxy state: %w", err)
	}
	forced := map[string]string{
		"ANTHROPIC_BASE_URL": binding.Root, "ANTHROPIC_AUTH_TOKEN": binding.Token,
		"CLAUDE_CONFIG_DIR": dir, "ANTHROPIC_API_KEY": "", "CLAUDE_CODE_OAUTH_TOKEN": "",
		"CLAUDE_CODE_OAUTH_TOKEN_FILE": "", "ANTHROPIC_CUSTOM_HEADERS": "X-Term-LLM-Proxy-App: term-llm",
		"CLAUDE_CODE_USE_BEDROCK": "0", "CLAUDE_CODE_USE_VERTEX": "0", "CLAUDE_CODE_USE_FOUNDRY": "0",
		"AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": "", "AWS_SESSION_TOKEN": "",
		"CLAUDE_CODE_API_KEY_HELPER_FD": "",
	}
	out := make([]string, 0, len(env)+len(forced))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == claudeProxyRootEnv {
			continue
		}
		if _, replace := forced[key]; !replace {
			out = append(out, entry)
		}
	}
	for key, value := range forced {
		out = append(out, key+"="+value)
	}
	return out, nil
}
