//go:build unix

package proxyconnect

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const fakeClaudeToken = "sk-ant-oat01-" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEfGhIjKlMnOpQrStUvWx-AA"

// fakeClaude writes an executable shell script standing in for the official
// CLI. It runs with an isolated HOME and never contacts anything.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s' \"$#:$*\" > \"$0.args\"\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertNoTokenLeak(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected failure")
	}
	if text := err.Error(); strings.Contains(text, "sk-ant") || strings.Contains(text, fakeClaudeToken[20:40]) {
		t.Fatalf("error leaks token material: %q", text)
	}
}

func TestAcquireClaudeTokenFromWrappedANSIOutput(t *testing.T) {
	half := len(fakeClaudeToken) / 2
	for name, body := range map[string]string{
		// Colored, hard-wrapped at the terminal width, written in chunks to stderr.
		"stderr wrapped": "printf '\\033[1mOpening browser...\\033[0m\\r\\n' >&2\nprintf '\\033]0;claude\\007Your token:\\n\\n  \\033[32m" + fakeClaudeToken[:20] + "' >&2\nsleep 0.05\nprintf '" + fakeClaudeToken[20:half] + "\\r\\n  " + fakeClaudeToken[half:] + "\\033[0m\\n\\nStore this token securely.\\n' >&2",
		"stdout plain":   "echo 'Token created.'\necho '" + fakeClaudeToken + "'\necho\necho 'Use it as CLAUDE_CODE_OAUTH_TOKEN.'",
	} {
		t.Run(name, func(t *testing.T) {
			path := fakeClaude(t, body)
			token, err := AcquireClaudeToken(context.Background(), path, strings.NewReader(""))
			if err != nil || token != fakeClaudeToken {
				t.Fatalf("token mismatch (err=%v, got %d bytes)", err, len(token))
			}
			if args, _ := os.ReadFile(path + ".args"); string(args) != "1:setup-token" {
				t.Fatalf("official executable ran with %q", args)
			}
		})
	}
}

func TestAcquireClaudeTokenPassesStdinAndIgnoresLingeringPipes(t *testing.T) {
	old := claudeTokenWaitDelay
	claudeTokenWaitDelay = 200 * time.Millisecond
	defer func() { claudeTokenWaitDelay = old }()
	path := fakeClaude(t, "read answer\n[ \"$answer\" = yes ] || exit 3\necho '"+fakeClaudeToken+"'")
	// The reader never reaches EOF after the answer, as a terminal would not.
	reader, writer := io.Pipe()
	defer writer.Close()
	go io.WriteString(writer, "yes\n")
	start := time.Now()
	token, err := AcquireClaudeToken(context.Background(), path, reader)
	if err != nil || token != fakeClaudeToken {
		t.Fatalf("stdin was not passed through: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("finished sign-in kept waiting on stdin")
	}
}

func TestAcquireClaudeTokenFailsGenericallyWithoutLeaking(t *testing.T) {
	other := strings.Replace(fakeClaudeToken, "AbCd", "ZZZZ", 1)
	cases := map[string]string{
		"nonzero exit":       "echo '" + fakeClaudeToken + "'\nexit 1",
		"duplicate":          "echo '" + fakeClaudeToken + "'\necho '" + fakeClaudeToken + "' >&2",
		"two tokens":         "echo '" + fakeClaudeToken + "'\necho\necho '" + other + "'",
		"api key prefix":     "echo 'sk-ant-api03-" + fakeClaudeToken[13:] + "'",
		"too short":          "echo 'sk-ant-oat01-short'",
		"partial then death": "printf '" + fakeClaudeToken[:30] + "'\nkill -9 $$",
		"trailing text":      "echo '" + fakeClaudeToken + " (expires soon)'",
		"invalid character":  "echo '" + fakeClaudeToken[:40] + "+" + fakeClaudeToken[40:] + "'",
		"too long":           "printf '" + fakeClaudeToken + "'\nhead -c 2000 /dev/zero | tr '\\0' 'a'",
		"oversized output":   "head -c 300000 /dev/zero | tr '\\0' 'x'\necho\necho '" + fakeClaudeToken + "'",
		"no token":           "echo 'Login cancelled.'",
		"word under token":   "echo '" + fakeClaudeToken + "'\necho 'Done'",
		"long word under":    "echo '" + fakeClaudeToken + "'\necho 'Congratulationsyouaresignedin'",
		"short wrapped tail": "echo '" + fakeClaudeToken[:len(fakeClaudeToken)-3] + "'\necho '" + fakeClaudeToken[len(fakeClaudeToken)-3:] + "'",
		"split across pipes": "printf '" + fakeClaudeToken[:50] + "'\nprintf 'Warning: slow network\\n' >&2\nsleep 0.05\nprintf '" + fakeClaudeToken[50:] + "\\n'",
		"error text":         "echo 'failed: " + fakeClaudeToken + "' >&2\nexit 2",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			token, err := AcquireClaudeToken(context.Background(), fakeClaude(t, body), strings.NewReader(""))
			if token != "" || !errors.Is(err, ErrClaudeTokenUnavailable) {
				t.Fatalf("got %d token bytes, err=%v", len(token), err)
			}
			assertNoTokenLeak(t, err)
		})
	}
	for _, executable := range []string{"", filepath.Join(t.TempDir(), "missing-claude")} {
		if _, err := AcquireClaudeToken(context.Background(), executable, nil); !errors.Is(err, ErrClaudeTokenUnavailable) {
			t.Fatalf("executable %q: %v", executable, err)
		}
	}
}

func TestAcquireClaudeTokenCancellationKillsProcessTree(t *testing.T) {
	path := fakeClaude(t, "echo '"+fakeClaudeToken+"'\nsleep 60 &\necho $! > \"$0.child\"\nwait")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(path + ".child"); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	token, err := AcquireClaudeToken(ctx, path, strings.NewReader(""))
	if token != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sign-in returned %d token bytes, err=%v", len(token), err)
	}
	assertNoTokenLeak(t, err)
	if time.Since(start) > 10*time.Second {
		t.Fatal("cancellation did not stop the process promptly")
	}
	var pid int
	data, _ := os.ReadFile(path + ".child")
	for _, c := range strings.TrimSpace(string(data)) {
		pid = pid*10 + int(c-'0')
	}
	if pid == 0 {
		t.Fatal("fake child did not start")
	}
	for i := 0; i < 200; i++ {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		// A killed process whose new parent has not reaped it yet is a zombie.
		if stat, err := os.ReadFile("/proc/" + strings.TrimSpace(string(data)) + "/stat"); err == nil {
			if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && strings.HasPrefix(string(stat[i+1:]), " Z") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("orphaned child %d survived cancellation", pid)
}

func TestAcquireClaudeTokenHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := AcquireClaudeToken(ctx, fakeClaude(t, "echo '"+fakeClaudeToken+"'\nsleep 60"), strings.NewReader(""))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
