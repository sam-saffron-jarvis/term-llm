package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestProxyCommandIsolationAndHealth(t *testing.T) {
	oldRaw, oldCPU, oldMem, oldPprof := debugRaw, cpuProfile, memProfile, pprofFlag
	t.Cleanup(func() { debugRaw, cpuProfile, memProfile, pprofFlag = oldRaw, oldCPU, oldMem, oldPprof })
	for _, kind := range []string{"raw", "cpu", "heap", "pprof", "environment"} {
		t.Run(kind, func(t *testing.T) {
			debugRaw = false
			cpuProfile = ""
			memProfile = ""
			pprofFlag = ""
			t.Setenv("TERM_LLM_PPROF", "")
			profile := filepath.Join(t.TempDir(), "must-not-exist")
			switch kind {
			case "raw":
				debugRaw = true
			case "cpu":
				cpuProfile = profile
			case "heap":
				memProfile = profile
			case "pprof":
				pprofFlag = "0"
			case "environment":
				t.Setenv("TERM_LLM_PPROF", "1")
			}
			for _, args := range [][]string{{"proxy"}, {"proxy", "health"}} {
				root := &cobra.Command{Use: "test"}
				root.SetContext(context.Background())
				root.AddCommand(newServeProxyCommand())
				root.SetArgs(args)
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				err := root.Execute()
				if err == nil || !strings.Contains(err.Error(), "refuses") {
					t.Fatalf("%s %v: %v", kind, args, err)
				}
			}
			if _, err := os.Stat(profile); !os.IsNotExist(err) {
				t.Fatal("profiling started before rejection")
			}
		})
	}
}

func TestProxyPasskeyFlagsAndExplicitTokenMode(t *testing.T) {
	c := newServeProxyCommand()
	if got := c.Flags().Lookup("auth").DefValue; got != "passkey" {
		t.Fatalf("auth default = %q", got)
	}
	for _, name := range []string{"public-url", "passkey-bootstrap-token-file", "passkey-recovery-token-file", "admin-token-file"} {
		if c.Flags().Lookup(name) == nil {
			t.Fatalf("missing flag %s", name)
		}
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--state-dir", t.TempDir(), "--admin-token-file", "not-read"}, "requires explicit --auth token"},
		{[]string{"--state-dir", t.TempDir(), "--auth", "token"}, "--admin-token-file is required"},
		{[]string{"--state-dir", t.TempDir(), "--public-url", "https://proxy.example.test/custom/"}, "without a mount path"},
	} {
		root := &cobra.Command{Use: "test"}
		root.SetContext(context.Background())
		root.AddCommand(newServeProxyCommand())
		root.SetArgs(append([]string{"proxy"}, test.args...))
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v: %v", test.args, err)
		}
	}
}
