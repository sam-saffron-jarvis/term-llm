package tools

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gobwas/glob"
	"github.com/samsaffron/term-llm/internal/pathutil"
	"golang.org/x/sys/unix"
)

// shellNonceEnvVar is the environment variable name used to tag every process
// spawned by a tool command so descendants that escape the process group (via
// setsid, daemonisation, etc.) can still be reliably reaped on cleanup.
const shellNonceEnvVar = "TERMLLM_SHELL_NONCE"

const descendantLivenessGracePeriod = 20 * time.Millisecond

var taggedDescendantReaper = killTaggedDescendants

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int64
	total int64
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	if limit < 0 {
		limit = 0
	}
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	origLen := len(p)
	b.total += int64(origLen)

	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		return origLen, nil
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return origLen, nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.total > int64(b.buf.Len())
}

func prepareToolCommand(cmd *exec.Cmd, sweepTaggedDescendantsOnSuccess bool) (func(), error) {
	var stdinCloser func()
	if cmd.Stdin == nil {
		if devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0); err == nil {
			cmd.Stdin = devNull
			stdinCloser = func() { _ = devNull.Close() }
		}
	}

	configureCommandProcessGroup(cmd)

	var cancelCalled atomic.Bool
	if cmd.Cancel != nil {
		cancel := cmd.Cancel
		cmd.Cancel = func() error {
			cancelCalled.Store(true)
			return cancel()
		}
	}

	nonce := tagCommandWithNonce(cmd)
	probe, probeErr := installDescendantLivenessProbe(cmd)

	cleanup := func() {
		if probe != nil {
			probe.closeParentWriter()
		}

		// Tools must leave the world clean: after the command returns (success,
		// failure, timeout, or cancellation) reap every descendant it spawned.
		//
		// First pass: SIGKILL the process group so `nohup foo &` style children
		// that stayed in our pgid die immediately.
		var pgroupKillErr error
		if cmd.Process != nil {
			pgroupKillErr = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}

		// Second pass: only pay for the expensive /proc nonce sweep when we have
		// evidence the fast-path process-group reap was not enough, or when the
		// caller explicitly asked us to preserve the old always-sweep semantics.
		shouldSweepTaggedDescendants := cancelCalled.Load() || probeErr != nil || sweepTaggedDescendantsOnSuccess
		if !shouldSweepTaggedDescendants && pgroupKillErr != nil && !errors.Is(pgroupKillErr, syscall.ESRCH) {
			shouldSweepTaggedDescendants = true
		}
		if !shouldSweepTaggedDescendants && probe != nil && probe.descendantsLikelyAlive(descendantLivenessGracePeriod) {
			shouldSweepTaggedDescendants = true
		}
		if shouldSweepTaggedDescendants && nonce != "" {
			taggedDescendantReaper(nonce)
		}

		if probe != nil {
			probe.close()
		}
		if stdinCloser != nil {
			stdinCloser()
		}
	}
	return cleanup, nil
}

type descendantLivenessProbe struct {
	readEnd  *os.File
	writeEnd *os.File
}

func installDescendantLivenessProbe(cmd *exec.Cmd) (*descendantLivenessProbe, error) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, writeEnd)
	return &descendantLivenessProbe{readEnd: readEnd, writeEnd: writeEnd}, nil
}

func (p *descendantLivenessProbe) closeParentWriter() {
	if p == nil || p.writeEnd == nil {
		return
	}
	_ = p.writeEnd.Close()
	p.writeEnd = nil
}

func (p *descendantLivenessProbe) descendantsLikelyAlive(timeout time.Duration) bool {
	if p == nil || p.readEnd == nil {
		return false
	}

	pollTimeout := int(timeout / time.Millisecond)
	if timeout > 0 && pollTimeout == 0 {
		pollTimeout = 1
	}
	pollFds := []unix.PollFd{{Fd: int32(p.readEnd.Fd()), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
	n, err := unix.Poll(pollFds, pollTimeout)
	if err != nil {
		return true
	}
	if n == 0 {
		return true
	}
	return pollFds[0].Revents&unix.POLLHUP == 0
}

func (p *descendantLivenessProbe) close() {
	if p == nil {
		return
	}
	p.closeParentWriter()
	if p.readEnd != nil {
		_ = p.readEnd.Close()
		p.readEnd = nil
	}
}

// tagCommandWithNonce adds a unique TERMLLM_SHELL_NONCE=<hex> entry to cmd.Env
// so descendants can be identified later by scanning /proc. Returns the nonce
// value, or "" if it could not be generated (in which case tagging is silently
// skipped — the pgroup kill still runs).
func tagCommandWithNonce(cmd *exec.Cmd) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	nonce := hex.EncodeToString(buf[:])
	entry := shellNonceEnvVar + "=" + nonce
	if cmd.Env == nil {
		cmd.Env = append(os.Environ(), entry)
	} else {
		cmd.Env = append(cmd.Env, entry)
	}
	return nonce
}

// killTaggedDescendants SIGKILLs every process whose environment contains the
// given nonce. Safe no-op on systems without /proc (e.g. macOS in tests).
// We retry a handful of times with short sleeps to handle races where a
// descendant forks right as we scan.
func killTaggedDescendants(nonce string) {
	needle := []byte(shellNonceEnvVar + "=" + nonce)
	for attempt := 0; attempt < 3; attempt++ {
		pids := findPidsWithEnv(needle)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// findPidsWithEnv returns PIDs whose /proc/<pid>/environ contains needle.
// On systems without /proc, returns nil.
func findPidsWithEnv(needle []byte) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	selfPid := os.Getpid()
	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == selfPid {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		if bytes.Contains(data, needle) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// PrepareCommand configures a command for safe cancellation, including process-group teardown.
func PrepareCommand(cmd *exec.Cmd) (func(), error) {
	return prepareToolCommand(cmd, true)
}

func configureCommandProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// WaitDelay unblocks cmd.Wait() when the main process has exited but a
	// backgrounded descendant still holds the stdout/stderr pipe open. After
	// the delay Go closes the pipes and returns from Wait, preventing the
	// tool from hanging indefinitely.
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func splitShellWords(input string) ([]string, error) {
	var (
		args         []string
		current      strings.Builder
		inSingle     bool
		inDouble     bool
		escaped      bool
		tokenStarted bool
	)

	flush := func() {
		if tokenStarted {
			args = append(args, current.String())
			current.Reset()
			tokenStarted = false
		}
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			tokenStarted = true
			escaped = false

		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				current.WriteRune(r)
				tokenStarted = true
			}

		case inDouble:
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
				tokenStarted = true
			}

		default:
			switch r {
			case '\'':
				inSingle = true
				tokenStarted = true
			case '"':
				inDouble = true
				tokenStarted = true
			case '\\':
				escaped = true
				tokenStarted = true
			case ' ', '\t', '\n', '\r':
				flush()
			default:
				current.WriteRune(r)
				tokenStarted = true
			}
		}
	}

	if escaped || inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quoted argument")
	}
	flush()
	return args, nil
}

// SplitShellWords parses a command line into argv without invoking a shell.
func SplitShellWords(input string) ([]string, error) {
	return splitShellWords(input)
}

func hasUnsafeShellSyntax(input string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range input {
		switch {
		case escaped:
			escaped = false
			continue

		case inSingle:
			if r == '\'' {
				inSingle = false
			}
			continue

		case inDouble:
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			case '$', '`':
				return true
			}
			continue

		default:
			switch r {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '\\':
				escaped = true
			case ';', '|', '&', '<', '>', '(', ')', '\n', '\r', '$', '`':
				return true
			}
		}
	}

	return false
}

// HasUnsafeShellSyntax reports whether input uses shell operators that require a shell.
func HasUnsafeShellSyntax(input string) bool {
	return hasUnsafeShellSyntax(input)
}

func matchShellPattern(pattern, value string) bool {
	g, err := glob.Compile(pattern)
	if err != nil {
		return false
	}
	return g.Match(value)
}

func resolveToolPath(path string, isWrite bool) (string, error) {
	expanded, err := pathutil.Expand(path)
	if err != nil {
		return "", NewToolErrorf(ErrInvalidParams, "%v", err)
	}
	if isWrite {
		return canonicalizePathForWrite(expanded)
	}
	return canonicalizePath(expanded)
}
