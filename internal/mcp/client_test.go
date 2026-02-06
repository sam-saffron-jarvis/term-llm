package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreateStdioTransport_InheritsEnv(t *testing.T) {
	// Server with custom env should inherit parent PATH
	client := &Client{
		name: "test",
		config: ServerConfig{
			Command: "echo",
			Args:    []string{"hello"},
			Env: map[string]string{
				"CUSTOM_VAR": "custom_value",
			},
		},
	}

	transport := client.createStdioTransport(context.Background())
	ct, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatal("expected sdkmcp.CommandTransport")
	}

	env := ct.Command.Env
	if env == nil {
		t.Fatal("expected non-nil env when config has env vars")
	}

	// Check that parent PATH is inherited
	hasPath := false
	hasCustom := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if e == "CUSTOM_VAR=custom_value" {
			hasCustom = true
		}
	}

	if !hasPath {
		t.Error("parent PATH not inherited in subprocess env")
	}
	if !hasCustom {
		t.Error("custom env var not set")
	}
}

func TestCreateStdioTransport_NoEnvNil(t *testing.T) {
	// Server with no custom env should leave cmd.Env nil (inherit all)
	client := &Client{
		name: "test",
		config: ServerConfig{
			Command: "echo",
			Args:    []string{"hello"},
		},
	}

	transport := client.createStdioTransport(context.Background())
	ct, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatal("expected sdkmcp.CommandTransport")
	}

	if ct.Command.Env != nil {
		t.Error("expected nil env when no config env vars (inherits parent automatically)")
	}
}

func TestCreateStdioTransport_EmptyEnvNil(t *testing.T) {
	// Server with empty env map should also leave cmd.Env nil
	client := &Client{
		name: "test",
		config: ServerConfig{
			Command: "echo",
			Args:    []string{"hello"},
			Env:     map[string]string{},
		},
	}

	transport := client.createStdioTransport(context.Background())
	ct, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatal("expected sdkmcp.CommandTransport")
	}

	if ct.Command.Env != nil {
		t.Error("expected nil env when env map is empty")
	}
}

func TestCreateStdioTransport_EnvOverridesParent(t *testing.T) {
	// Set a known env var, then override it
	os.Setenv("TEST_MCP_VAR", "original")
	defer os.Unsetenv("TEST_MCP_VAR")

	client := &Client{
		name: "test",
		config: ServerConfig{
			Command: "echo",
			Env: map[string]string{
				"TEST_MCP_VAR": "overridden",
			},
		},
	}

	transport := client.createStdioTransport(context.Background())
	ct := transport.(*sdkmcp.CommandTransport)

	// The overridden value should appear (last wins in exec.Cmd)
	found := false
	for _, e := range ct.Command.Env {
		if e == "TEST_MCP_VAR=overridden" {
			found = true
		}
	}
	if !found {
		t.Error("expected overridden env var in subprocess env")
	}
}
