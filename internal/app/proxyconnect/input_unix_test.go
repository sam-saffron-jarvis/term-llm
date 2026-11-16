//go:build unix

package proxyconnect

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func TestMaskedSubscriptionPasteAndCtrlCRestoreTerminal(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "paste", true: "cancel"}[cancel], func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Skip("PTY unavailable", err)
			}
			defer master.Close()
			defer slave.Close()
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ready := make(chan struct{})
			output := make(chan []byte, 1)
			go func() {
				var captured bytes.Buffer
				buf := make([]byte, 1024)
				signaled := false
				for {
					n, e := master.Read(buf)
					if n > 0 {
						captured.Write(buf[:n])
						if !signaled && bytes.Contains(captured.Bytes(), []byte("hidden")) {
							close(ready)
							signaled = true
						}
					}
					if e != nil {
						output <- captured.Bytes()
						return
					}
				}
			}()
			done := make(chan struct {
				token string
				err   error
			}, 1)
			r := &connectorRun{opts: Options{In: slave, Out: slave}}
			go func() {
				token, e := r.readSubscriptionToken()
				done <- struct {
					token string
					err   error
				}{token, e}
			}()
			select {
			case <-ready:
			case <-time.After(time.Second):
				t.Fatal("masked prompt unavailable")
			}
			secret := "sk-ant-oat01-" + strings.Repeat("fixture", 8)
			if cancel {
				master.Write([]byte{3})
			} else {
				master.Write([]byte(secret + "\r"))
			}
			select {
			case result := <-done:
				if cancel && result.err == nil || !cancel && (result.err != nil || result.token != secret) {
					t.Fatal("masked read/cancel failed")
				}
			case <-time.After(time.Second):
				t.Fatal("masked input did not complete")
			}
			after, e := term.GetState(int(slave.Fd()))
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("terminal mode not restored", e)
			}
			slave.Close()
			select {
			case raw := <-output:
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatal("subscription token echoed")
				}
			case <-time.After(time.Second):
				t.Fatal("PTY output did not close")
			}
		})
	}
}
func TestNativeSignInInterruptUsesOwnedContext(t *testing.T) {
	if os.Getenv("TERM_LLM_TEST_CLAUDE_SIGNAL_HELPER") == "1" {
		r := &connectorRun{opts: Options{Out: io.Discard, Confirm: func(string) (bool, error) { return true, nil }, AcquireToken: func(ctx context.Context, _ string, _ io.Reader) (string, error) {
			fmt.Fprintln(os.Stdout, "ready")
			<-ctx.Done()
			return "", ctx.Err()
		}}}
		_, err := r.subscriptionToken(context.Background(), ConnectRequest{ResolvedAction: "add", Expires: time.Now().Add(time.Minute).Unix()})
		if err == nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestNativeSignInInterruptUsesOwnedContext$")
	cmd.Env = append(os.Environ(), "TERM_LLM_TEST_CLAUDE_SIGNAL_HELPER=1")
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatal("native signal helper did not start")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native signal helper did not start")
	}
	if err = cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("interrupt did not cancel owned context", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt did not finish")
	}
}
