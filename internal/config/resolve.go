package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/samsaffron/term-llm/internal/procutil"
)

var (
	resolveExecTimeout           = 15 * time.Second
	resolveExecOutputLimit int64 = 64 << 10
	resolveExecWaitDelay         = time.Second
)

// ResolveValue handles magic URL schemes in config values:
// - op://vault/item/field -> 1Password secret (via `op read`)
// - srv://record/path -> DNS SRV lookup + path (always HTTPS)
// - $(...) -> shell command output
// - ${VAR} or $VAR -> environment variable
// - literal string -> returned as-is
func ResolveValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	switch {
	case strings.HasPrefix(value, "op://"):
		return resolveOnePassword(value)
	case strings.HasPrefix(value, "srv://"):
		return resolveSRV(value)
	case strings.HasPrefix(value, "$(") && strings.HasSuffix(value, ")"):
		return resolveCommand(value[2 : len(value)-1])
	default:
		return expandEnv(value), nil
	}
}

// resolveOnePassword handles op:// URLs via `op read`
// Format: op://vault/item/field or op://vault/item/field?account=account.1password.com
func resolveOnePassword(opURL string) (string, error) {
	// Parse URL to extract account query parameter if present
	u, err := url.Parse(opURL)
	if err != nil {
		return "", fmt.Errorf("1password: invalid URL %s: %w", opURL, err)
	}

	account := u.Query().Get("account")

	// Reconstruct the op:// URL without query params for op read
	cleanURL := fmt.Sprintf("op://%s%s", u.Host, u.Path)

	// op read supports the op:// format directly
	args := []string{"read", cleanURL}
	if account != "" {
		args = append(args, "--account", account)
	}

	output, err := runResolverCommand("op", args...)
	if err != nil {
		return "", fmt.Errorf("1password: failed to read %s: %w (is 'op' CLI installed and signed in?)", cleanURL, err)
	}
	return output, nil
}

// resolveSRV handles srv://_service._proto.domain/path URLs
// Returns https://host:port/path
func resolveSRV(srvURL string) (string, error) {
	// Parse: srv://_vllm-llama-large._tcp.ai.snorlax.discourse.com/v1/chat/completions
	u, err := url.Parse(srvURL)
	if err != nil {
		return "", fmt.Errorf("invalid srv:// URL: %w", err)
	}

	record := u.Host // _vllm-llama-large._tcp.ai.snorlax.discourse.com
	path := u.Path   // /v1/chat/completions

	if record == "" {
		return "", fmt.Errorf("srv:// URL missing host: %s", srvURL)
	}

	// Lookup SRV record
	_, addrs, err := net.LookupSRV("", "", record)
	if err != nil {
		return "", fmt.Errorf("SRV lookup failed for %s: %w", record, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no SRV records found for %s", record)
	}

	// Use first record (sorted by priority/weight by Go's DNS resolver)
	addr := addrs[0]
	host := strings.TrimSuffix(addr.Target, ".")

	return fmt.Sprintf("https://%s:%d%s", host, addr.Port, path), nil
}

// resolveCommand executes a shell command and returns its output
func resolveCommand(cmd string) (string, error) {
	output, err := runResolverCommand("sh", "-c", cmd)
	if err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}
	return output, nil
}

func runResolverCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = resolveExecWaitDelay

	cleanup, prepErr := procutil.PrepareCommand(cmd)
	if prepErr != nil {
		return "", fmt.Errorf("command setup failed: %w", prepErr)
	}
	defer cleanup()

	stdout := procutil.NewLimitedBuffer(resolveExecOutputLimit)
	stderr := procutil.NewLimitedBuffer(resolveExecOutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("timed out after %s", resolveExecTimeout)
	}
	if stdout.Truncated() || stderr.Truncated() {
		return "", fmt.Errorf("output exceeded %d bytes", resolveExecOutputLimit)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = strings.TrimSpace(stdout.String())
			}
			if msg == "" {
				msg = exitErr.Error()
			}
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}

	return strings.TrimSpace(stdout.String()), nil
}
