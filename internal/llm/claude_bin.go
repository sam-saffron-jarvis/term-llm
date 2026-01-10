package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ClaudeBinProvider implements Provider using the claude CLI binary.
// This provider shells out to the claude command for inference,
// using Claude Code's existing authentication.
type ClaudeBinProvider struct {
	model     string
	sessionID string // For session continuity with --resume
}

// NewClaudeBinProvider creates a new provider that uses the claude binary.
func NewClaudeBinProvider(model string) *ClaudeBinProvider {
	return &ClaudeBinProvider{
		model: model,
	}
}

func (p *ClaudeBinProvider) Name() string {
	model := p.model
	if model == "" {
		model = "sonnet"
	}
	return fmt.Sprintf("Claude CLI (%s)", model)
}

func (p *ClaudeBinProvider) Credential() string {
	return "claude-bin"
}

func (p *ClaudeBinProvider) Capabilities() Capabilities {
	return Capabilities{
		NativeWebSearch: true,
		NativeWebFetch:  true,
		ToolCalls:       true,
	}
}

func (p *ClaudeBinProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	return newEventStream(ctx, func(ctx context.Context, events chan<- Event) error {
		// Build the command arguments
		args := p.buildArgs(req)

		// Build the prompt from messages
		prompt := p.buildPrompt(req.Messages)

		// Add prompt as positional argument
		args = append(args, "--", prompt)

		if req.Debug {
			fmt.Fprintln(os.Stderr, "=== DEBUG: Claude CLI Command ===")
			fmt.Fprintf(os.Stderr, "claude %s\n", strings.Join(args, " "))
			fmt.Fprintln(os.Stderr, "=================================")
		}

		cmd := exec.CommandContext(ctx, "claude", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to get stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start claude: %w", err)
		}

		scanner := bufio.NewScanner(stdout)
		// Increase buffer size for large JSON messages
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

		var lastUsage *Usage

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			// Parse the message type first
			var baseMsg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
				if req.Debug {
					fmt.Fprintf(os.Stderr, "Failed to parse JSON: %s\n", line[:min(100, len(line))])
				}
				continue
			}

			switch baseMsg.Type {
			case "system":
				// Extract session ID for potential resume
				var sysMsg claudeSystemMessage
				if err := json.Unmarshal([]byte(line), &sysMsg); err == nil {
					p.sessionID = sysMsg.SessionID
					if req.Debug {
						fmt.Fprintf(os.Stderr, "Session: %s, Model: %s, Tools: %v\n",
							sysMsg.SessionID, sysMsg.Model, sysMsg.Tools)
					}
				}

			case "assistant":
				var assistantMsg claudeAssistantMessage
				if err := json.Unmarshal([]byte(line), &assistantMsg); err != nil {
					continue
				}

				for _, content := range assistantMsg.Message.Content {
					switch content.Type {
					case "text":
						if content.Text != "" {
							events <- Event{Type: EventTextDelta, Text: content.Text}
						}
					case "tool_use":
						// Convert to term-llm tool call format
						toolCall := ToolCall{
							ID:        content.ID,
							Name:      mapClaudeToolName(content.Name),
							Arguments: content.Input,
						}
						events <- Event{Type: EventToolCall, Tool: &toolCall}
					}
				}

			case "result":
				var resultMsg claudeResultMessage
				if err := json.Unmarshal([]byte(line), &resultMsg); err == nil {
					lastUsage = &Usage{
						InputTokens:  resultMsg.Usage.InputTokens + resultMsg.Usage.CacheReadInputTokens,
						OutputTokens: resultMsg.Usage.OutputTokens,
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error reading claude output: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("claude command failed: %w", err)
		}

		if lastUsage != nil {
			events <- Event{Type: EventUsage, Use: lastUsage}
		}
		events <- Event{Type: EventDone}
		return nil
	}), nil
}

// buildArgs constructs the command line arguments for the claude binary.
func (p *ClaudeBinProvider) buildArgs(req Request) []string {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
	}

	// For native search, allow more turns so claude can execute the search
	// For other tool calls, limit to 1 turn so term-llm can handle execution
	if req.Search && !req.ForceExternalSearch {
		args = append(args, "--max-turns", "5") // Allow search execution
	} else {
		args = append(args, "--max-turns", "1") // Single turn - let term-llm handle tool execution
	}

	// Model selection
	model := chooseModel(req.Model, p.model)
	if model != "" {
		args = append(args, "--model", mapModelToClaudeArg(model))
	}

	// Tool configuration
	if len(req.Tools) > 0 {
		toolNames := mapToolsToClaudeNames(req.Tools, req.Search)
		if len(toolNames) > 0 {
			args = append(args, "--tools", strings.Join(toolNames, ","))
		}
	} else if req.Search {
		// If search is requested but no tools, add just WebSearch
		args = append(args, "--tools", "WebSearch")
	} else {
		// No tools requested - disable all
		args = append(args, "--tools", "")
	}

	// Session resume for multi-turn conversations
	if p.sessionID != "" {
		args = append(args, "--resume", p.sessionID)
	}

	return args
}

// buildPrompt constructs a prompt string from term-llm messages.
func (p *ClaudeBinProvider) buildPrompt(messages []Message) string {
	var systemParts []string
	var conversationParts []string

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			systemParts = append(systemParts, collectTextParts(msg.Parts))
		case RoleUser:
			text := collectTextParts(msg.Parts)
			if text != "" {
				conversationParts = append(conversationParts, "User: "+text)
			}
		case RoleAssistant:
			text := collectTextParts(msg.Parts)
			// Also capture tool calls from assistant
			for _, part := range msg.Parts {
				if part.Type == PartToolCall && part.ToolCall != nil {
					conversationParts = append(conversationParts,
						fmt.Sprintf("Assistant called tool: %s", part.ToolCall.Name))
				}
			}
			if text != "" {
				conversationParts = append(conversationParts, "Assistant: "+text)
			}
		case RoleTool:
			// Format tool results
			for _, part := range msg.Parts {
				if part.Type == PartToolResult && part.ToolResult != nil {
					conversationParts = append(conversationParts,
						fmt.Sprintf("Tool result (%s): %s", part.ToolResult.Name, part.ToolResult.Content))
				}
			}
		}
	}

	// Build final prompt
	var prompt strings.Builder

	// Add system context if present
	if len(systemParts) > 0 {
		prompt.WriteString(strings.Join(systemParts, "\n\n"))
		prompt.WriteString("\n\n")
	}

	// Add conversation history
	if len(conversationParts) > 0 {
		prompt.WriteString(strings.Join(conversationParts, "\n\n"))
	}

	return strings.TrimSpace(prompt.String())
}

// mapModelToClaudeArg converts a model name to claude CLI argument.
func mapModelToClaudeArg(model string) string {
	model = strings.ToLower(model)
	if strings.Contains(model, "opus") {
		return "opus"
	}
	if strings.Contains(model, "haiku") {
		return "haiku"
	}
	// Default to sonnet
	return "sonnet"
}

// mapToolsToClaudeNames converts term-llm tool specs to claude tool names.
func mapToolsToClaudeNames(tools []ToolSpec, includeSearch bool) []string {
	claudeToolMap := map[string]string{
		"read_file":   "Read",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"execute":     "Bash",
		"glob":        "Glob",
		"grep":        "Grep",
		"web_search":  "WebSearch",
		"web_fetch":   "WebFetch",
		"todo_write":  "TodoWrite",
		"ask_user":    "AskUserQuestion",
		"notebook":    "NotebookEdit",
	}

	seen := make(map[string]bool)
	var names []string

	for _, tool := range tools {
		// Try mapping, otherwise use the name directly (claude might have it)
		claudeName := tool.Name
		if mapped, ok := claudeToolMap[strings.ToLower(tool.Name)]; ok {
			claudeName = mapped
		}
		if !seen[claudeName] {
			seen[claudeName] = true
			names = append(names, claudeName)
		}
	}

	// Add WebSearch if search is requested and not already included
	if includeSearch && !seen["WebSearch"] {
		names = append(names, "WebSearch")
	}

	return names
}

// mapClaudeToolName converts claude tool names back to term-llm names.
func mapClaudeToolName(claudeName string) string {
	// Map claude names to term-llm names
	reverseMap := map[string]string{
		"Read":            "read_file",
		"Write":           "write_file",
		"Edit":            "edit_file",
		"Bash":            "execute",
		"Glob":            "glob",
		"Grep":            "grep",
		"WebSearch":       "web_search",
		"WebFetch":        "web_fetch",
		"TodoWrite":       "todo_write",
		"AskUserQuestion": "ask_user",
		"NotebookEdit":    "notebook",
	}

	if mapped, ok := reverseMap[claudeName]; ok {
		return mapped
	}
	return claudeName
}

// JSON message types from claude CLI output

type claudeSystemMessage struct {
	Type      string   `json:"type"`
	SessionID string   `json:"session_id"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools"`
}

type claudeAssistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeResultMessage struct {
	Type  string `json:"type"`
	Usage struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}
