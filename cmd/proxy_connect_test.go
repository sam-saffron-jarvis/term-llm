package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestProxyConnectClaudeCommandIsIsolatedAndHasNoCredentialArgument(t *testing.T) {
	root := newProxyCommand()
	leaf, args, err := root.Find([]string{"connect", "claude"})
	if err != nil || len(args) != 0 || leaf.Annotations["isolated-startup"] != "true" {
		t.Fatal("Claude connect must use isolated startup", err, args)
	}
	for _, name := range []string{"url", "account", "name", "reconnect", "paste-token", "claude-command", "no-browser", "cancel"} {
		if leaf.Flags().Lookup(name) == nil {
			t.Fatal("missing flag", name)
		}
	}
	for _, name := range []string{"token", "admin-token", "access-token", "refresh-token", "proxy-all-providers"} {
		if leaf.Flags().Lookup(name) != nil {
			t.Fatal("credential/global rebind flag is forbidden", name)
		}
	}
	undo, _, err := root.Find([]string{"disconnect", "claude"})
	if err != nil || undo.Annotations["isolated-startup"] != "true" {
		t.Fatal("missing isolated Claude undo")
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"connect", "claude", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--paste-token") || strings.Contains(out.String(), "--admin-token") {
		t.Fatal("unsafe/incomplete help", out.String())
	}
}
