package proxyconnect

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/term-llm/term-llm/internal/procutil"
)

const (
	claudeTokenPrefix      = "sk-ant-oat01-"
	claudeTokenOutputLimit = 256 << 10
	claudeTokenMinLength   = len(claudeTokenPrefix) + 20
	claudeTokenMaxLength   = 1024
	claudeTokenTimeout     = 10 * time.Minute
	claudeTokenDefaultWait = 2 * time.Second
)

// claudeTokenWaitDelay bounds how long a finished process may keep its pipes busy.
var claudeTokenWaitDelay = claudeTokenDefaultWait

// ErrClaudeTokenUnavailable is the only failure AcquireClaudeToken reports
// besides context cancellation. It deliberately carries no process output.
var ErrClaudeTokenUnavailable = errors.New("Claude sign-in did not produce exactly one usable setup token")

type lockedOutput struct {
	mu  sync.Mutex
	buf *procutil.LimitedBuffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

// AcquireClaudeToken runs `<executable> setup-token` and returns the single
// Claude OAuth setup token it prints. The caller must have obtained the user's
// permission first. stdin is passed through (pass an *os.File such as
// os.Stdin: any other reader is copied by a goroutine that may consume input
// arriving after the process exits); stdout and stderr are captured
// privately (at most 256 KiB) and are never echoed or included in an error, so
// any sign-in URL the process prints is not shown: the browser it opens is the
// only interactive surface. No expiry is inferred from the opaque token.
//
// Without a caller deadline the process is given ten minutes. The process
// tree is terminated on cancellation and before returning.
func AcquireClaudeToken(ctx context.Context, executable string, stdin io.Reader) (string, error) {
	if strings.TrimSpace(executable) == "" {
		return "", ErrClaudeTokenUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, claudeTokenTimeout)
		defer cancel()
	}
	output := &lockedOutput{buf: procutil.NewLimitedBuffer(claudeTokenOutputLimit)}
	cmd := exec.CommandContext(ctx, executable, "setup-token")
	cmd.Stdout, cmd.Stderr = output, output
	// A browser helper that keeps the output pipe open must not keep a
	// finished sign-in waiting.
	cmd.WaitDelay = claudeTokenWaitDelay
	var feed io.WriteCloser
	if file, ok := stdin.(*os.File); ok {
		cmd.Stdin = file // Inherited directly; nothing is read on its behalf.
	} else if stdin != nil {
		// os/exec would wait for its own copy of a reader that never reaches
		// EOF, so the copy is owned here and never awaited.
		pipe, err := cmd.StdinPipe()
		if err != nil {
			return "", ErrClaudeTokenUnavailable
		}
		feed = pipe
	}
	owned, err := procutil.PrepareCommand(cmd)
	if err != nil {
		return "", ErrClaudeTokenUnavailable
	}
	defer owned.Close()
	if err = owned.Start(); err == nil {
		if feed != nil {
			go func() {
				_, _ = io.Copy(feed, stdin)
				_ = feed.Close()
			}()
		}
		err = owned.Wait()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	exited := cmd.ProcessState != nil && cmd.ProcessState.Success()
	if !exited || err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return "", ErrClaudeTokenUnavailable
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.buf.Truncated() {
		return "", ErrClaudeTokenUnavailable
	}
	token, ok := parseClaudeSetupToken(output.buf.String())
	if !ok {
		return "", ErrClaudeTokenUnavailable
	}
	return token, nil
}

// parseClaudeSetupToken accepts output that contains the setup-token prefix
// exactly once. A terminal line wrap inside the token is rejoined only when
// the following line is unmistakably token material; an ambiguous following
// line fails rather than risking a wrong token.
func parseClaudeSetupToken(output string) (string, bool) {
	text := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(stripTerminalControls(output))
	if strings.Count(text, claudeTokenPrefix) != 1 {
		return "", false
	}
	lines := strings.Split(text[strings.Index(text, claudeTokenPrefix):], "\n")
	first := claudeTokenRun(lines[0])
	if strings.TrimSpace(lines[0][len(first):]) != "" {
		return "", false // The token is not the end of its line.
	}
	token := first
	for _, line := range lines[1:] {
		part := strings.TrimSpace(line)
		if part == "" || claudeTokenRun(part) != part {
			break
		}
		// A bare word directly under the token could be prose or a wrapped
		// tail; that cannot be told apart, so it is not guessed.
		if !claudeTokenContinuation(part) {
			return "", false
		}
		token += part
		if len(token) > claudeTokenMaxLength {
			return "", false
		}
	}
	if len(token) < claudeTokenMinLength || len(token) > claudeTokenMaxLength {
		return "", false
	}
	return token, true
}

// claudeTokenContinuation reports whether a token-character-only line is
// unmistakably opaque token material rather than a word: long enough, and
// mixing letters with digits or separators.
func claudeTokenContinuation(part string) bool {
	return len(part) >= 16 && strings.ContainsAny(part, "0123456789-_") && strings.ContainsFunc(part, func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	})
}

// claudeTokenRun returns the leading run of token characters.
func claudeTokenRun(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return s[:i]
		}
	}
	return s
}

// stripTerminalControls removes ANSI CSI, OSC and two-byte escape sequences.
func stripTerminalControls(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[':
			for i++; i < len(s) && (s[i] < 0x40 || s[i] > 0x7e); i++ {
			}
		case ']':
			for i++; i < len(s) && s[i] != 0x07; i++ {
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
			}
		}
	}
	return b.String()
}
