package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolSpec describes a tool available from an MCP server.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Client wraps an MCP server connection.
type Client struct {
	name    string
	config  ServerConfig
	client  *mcp.Client
	session *mcp.ClientSession
	tools   []ToolSpec
	mu      sync.RWMutex
	running bool
}

// NewClient creates a new MCP client for the given server configuration.
func NewClient(name string, config ServerConfig) *Client {
	return &Client{
		name:   name,
		config: config,
	}
}

// Name returns the server name.
func (c *Client) Name() string {
	return c.name
}

// Start connects to the MCP server and initializes the session.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	// Create the MCP client
	c.client = mcp.NewClient(&mcp.Implementation{
		Name:    "term-llm",
		Version: "1.0.0",
	}, nil)

	// Build command with environment
	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...)
	for k, v := range c.config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Connect via stdio transport
	transport := &mcp.CommandTransport{Command: cmd}
	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to MCP server %s: %w", c.name, err)
	}
	c.session = session

	// Fetch available tools
	if err := c.refreshTools(ctx); err != nil {
		c.session.Close()
		c.session = nil
		return fmt.Errorf("list tools from %s: %w", c.name, err)
	}

	c.running = true
	return nil
}

// Stop closes the MCP server connection.
func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	var err error
	if c.session != nil {
		err = c.session.Close()
		c.session = nil
	}
	c.running = false
	c.tools = nil
	return err
}

// IsRunning returns whether the client is connected.
func (c *Client) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// Tools returns the available tools from this server.
func (c *Client) Tools() []ToolSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tools
}

// refreshTools fetches the tool list from the server.
func (c *Client) refreshTools(ctx context.Context) error {
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return err
	}

	c.tools = make([]ToolSpec, 0, len(result.Tools))
	for _, t := range result.Tools {
		schema := make(map[string]any)
		if t.InputSchema != nil {
			if m, ok := t.InputSchema.(map[string]any); ok {
				schema = m
			}
		}
		c.tools = append(c.tools, ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Schema:      schema,
		})
	}
	return nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	c.mu.RLock()
	session := c.session
	running := c.running
	c.mu.RUnlock()

	if !running || session == nil {
		return "", fmt.Errorf("MCP server %s is not running", c.name)
	}

	// Parse arguments
	var arguments map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return "", fmt.Errorf("invalid tool arguments: %w", err)
		}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}

	if result.IsError {
		return "", fmt.Errorf("tool %s returned error: %s", name, formatContent(result.Content))
	}

	return formatContent(result.Content), nil
}

// formatContent converts MCP content to a string.
func formatContent(content []mcp.Content) string {
	var result string
	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			result += v.Text
		default:
			// For other content types, try JSON encoding
			if data, err := json.Marshal(c); err == nil {
				result += string(data)
			}
		}
	}
	return result
}
