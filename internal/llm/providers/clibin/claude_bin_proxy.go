package clibin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/term-llm/term-llm/internal/cliwire"
	"github.com/term-llm/term-llm/internal/procutil"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

const claudeProxyRootEnv = "TERM_LLM_CLAUDE_PROXY_ROOT"

// prepareClaudeCommand also returns the account whose plan the process reports
// on, or "" when its rate_limit_event readings cannot be attributed.
func (p *ClaudeBinProvider) prepareClaudeCommand(ctx context.Context, args []string, effort, workingDir string) (*exec.Cmd, io.WriteCloser, *procutil.Command, string, error) {
	cmd, err := newCLICommand(ctx, "claude", args, workingDir)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("prepare claude command: %w", err)
	}
	cmd.WaitDelay = claudeCommandWaitDelay
	cmd.Env = p.buildCommandEnv(effort)
	// Read the account before launch preparation strips the proxy route marker.
	account := p.claudeLaunchAccount(cmd.Env)
	cleanup, err := prepareClaudeLaunch(ctx, "claude-bin", cmd)
	if err != nil {
		return nil, nil, nil, "", err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, nil, nil, "", fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	owned, err := procutil.PrepareCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		cleanup()
		return nil, nil, nil, "", fmt.Errorf("failed to prepare claude command: %w", err)
	}
	owned.SetCloseHook(cleanup)
	return cmd, stdin, owned, account, nil
}

// prepareClaudeLaunch applies the final proxy environment and the credential
// descriptor to the command used for inference, compaction, or model listing.
func prepareClaudeLaunch(ctx context.Context, provider string, cmd *exec.Cmd) (func(), error) {
	cleanup, err := prepareClaudeProxyCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		// Wire audit replaces the CONNECT proxy instead of chaining through it.
		// A shared placeholder must never be routed away from its scoped proxy.
		if strings.TrimSpace(envValue(cmd.Env, cliwire.TraceRootEnv)) != "" {
			cleanup()
			return nil, errors.New("wire audit cannot be chained with the shared Claude proxy; unset " + cliwire.TraceRootEnv + " (no direct fallback)")
		}
		return cleanup, nil
	}
	wireAudit, err := startCLIWireServer(provider, cmd.Env)
	if err != nil {
		return nil, err
	}
	if wireAudit != nil {
		cmd.Env = forceCLIWireEnvironment(cmd.Env, wireAudit.Environment())
	}
	return func() { stopCLIWireAudit(wireAudit) }, nil
}

func prepareClaudeProxyCommand(ctx context.Context, cmd *exec.Cmd) (func(), error) {
	root := envValue(cmd.Env, claudeProxyRootEnv)
	if root == "" {
		return nil, nil
	}
	binding, ok := proxyclient.Lookup(root)
	if !ok || binding.Kind != "claude-bin" || !proxyclient.SameRoot(binding.Root, root) ||
		proxyclient.ValidateClaudeNativeToken(binding.Token) != nil {
		return nil, errors.New("claude-bin proxy root has no scoped credential binding")
	}
	stateDir, err := proxyclient.ClaudeNativeStateDir(binding.Root, binding.Token)
	if err != nil {
		return nil, err
	}
	return proxyclient.PrepareClaudeNativeCommand(ctx, cmd, binding.Root, binding.Token, stateDir)
}
