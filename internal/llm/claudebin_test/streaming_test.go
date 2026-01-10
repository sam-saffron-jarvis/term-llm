// Package claudebin_test contains standalone tests for claude binary integration.
// Run with: go test -v ./internal/llm/claudebin_test/...
package claudebin_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Message types from claude SDK
type SystemMessage struct {
	Type      string   `json:"type"`
	SessionID string   `json:"session_id"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools"` // Tool names as strings, not objects
}

type AssistantMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
	} `json:"message"`
}

type ResultMessage struct {
	Type  string `json:"type"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Result struct {
		CostUSD float64 `json:"cost_usd"`
	} `json:"result"`
}

// TestBasicStreaming verifies we can call claude binary and parse streaming JSON
func TestBasicStreaming(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--", "Say 'Hello' and nothing else.")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var messages []string
	var gotSystem, gotAssistant, gotResult bool

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		messages = append(messages, line)

		// Parse to determine message type
		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			t.Logf("Failed to parse line: %s", line)
			continue
		}

		switch baseMsg.Type {
		case "system":
			gotSystem = true
			var msg SystemMessage
			json.Unmarshal([]byte(line), &msg)
			t.Logf("System: session=%s model=%s tools=%v", msg.SessionID, msg.Model, msg.Tools)
		case "assistant":
			gotAssistant = true
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "text" {
					t.Logf("Assistant text: %s", c.Text)
				} else if c.Type == "tool_use" {
					t.Logf("Assistant tool_use: %s(%s)", c.Name, string(c.Input))
				}
			}
		case "result":
			gotResult = true
			var msg ResultMessage
			json.Unmarshal([]byte(line), &msg)
			t.Logf("Result: in=%d out=%d cost=$%.6f", msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Result.CostUSD)
		}
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("Claude command failed: %v", err)
	}

	if !gotSystem {
		t.Error("Did not receive system message")
	}
	if !gotAssistant {
		t.Error("Did not receive assistant message")
	}
	if !gotResult {
		t.Error("Did not receive result message")
	}

	t.Logf("Total messages: %d", len(messages))
}

// TestToolCallParsing verifies we can parse tool calls from assistant messages
func TestToolCallParsing(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ask claude to read a file - this should trigger a tool call
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--", "Read the file /etc/hosts and tell me what's in it.")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var toolCalls []struct {
		ID    string
		Name  string
		Input json.RawMessage
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			continue
		}

		if baseMsg.Type == "assistant" {
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "tool_use" {
					toolCalls = append(toolCalls, struct {
						ID    string
						Name  string
						Input json.RawMessage
					}{c.ID, c.Name, c.Input})
					t.Logf("Found tool call: %s (id=%s) args=%s", c.Name, c.ID, string(c.Input))
				}
			}
		}
	}

	cmd.Wait()

	if len(toolCalls) == 0 {
		t.Error("Expected at least one tool call, got none")
	} else {
		t.Logf("Found %d tool calls", len(toolCalls))
	}
}

// TestToolsFlag verifies --tools filtering works
func TestToolsFlag(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use --tools to restrict to only Read
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--tools", "Read",
		"--", "What tools do you have access to?")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var toolNames []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			continue
		}

		if baseMsg.Type == "system" {
			var msg SystemMessage
			json.Unmarshal([]byte(line), &msg)
			toolNames = msg.Tools // Tools is []string now
			t.Logf("Available tools: %v", toolNames)
		}
	}

	cmd.Wait()

	// Verify only Read is available
	if len(toolNames) == 0 {
		t.Error("No tools reported in system message")
	}

	if len(toolNames) != 1 || toolNames[0] != "Read" {
		t.Errorf("Expected only [Read], got %v", toolNames)
	}
}

// TestDisallowedTools verifies --disallowed-tools filtering works
func TestDisallowedTools(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Disable Bash and Write tools
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--disallowed-tools", "Bash,Write,Edit",
		"--", "What tools do you have?")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var toolNames []string
	disallowed := map[string]bool{"Bash": true, "Write": true, "Edit": true}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			continue
		}

		if baseMsg.Type == "system" {
			var msg SystemMessage
			json.Unmarshal([]byte(line), &msg)
			toolNames = msg.Tools
			for _, name := range toolNames {
				if disallowed[name] {
					t.Errorf("Disallowed tool still present: %s", name)
				}
			}
			t.Logf("Available tools: %v", toolNames)
		}
	}

	cmd.Wait()
}

// TestEmptyTools verifies we can disable all tools with --tools ""
func TestEmptyTools(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Disable all tools with empty string
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--tools", "",
		"--", "Say hello")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	// Read stderr for error messages
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			t.Logf("stderr: %s", scanner.Text())
		}
	}()

	scanner := bufio.NewScanner(stdout)
	var toolCount int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			continue
		}

		if baseMsg.Type == "system" {
			var msg SystemMessage
			json.Unmarshal([]byte(line), &msg)
			toolCount = len(msg.Tools)
			t.Logf("Tool count with --tools \"\": %d", toolCount)
			for _, name := range msg.Tools {
				t.Logf("  - %s", name)
			}
		}
	}

	cmd.Wait()

	if toolCount != 0 {
		t.Errorf("Expected 0 tools with --tools \"\", got %d", toolCount)
	}
}

// TestSystemPrompt verifies --system-prompt flag works
func TestSystemPrompt(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	systemPrompt := "You are a pirate. Always respond like a pirate."

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--system-prompt", systemPrompt,
		"--", "Say hello")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start claude: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	var responseText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &baseMsg); err != nil {
			continue
		}

		if baseMsg.Type == "assistant" {
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "text" {
					responseText.WriteString(c.Text)
				}
			}
		}
	}

	cmd.Wait()

	response := responseText.String()
	t.Logf("Response with pirate system prompt: %s", response)

	// Check for pirate-like language (loose check)
	pirateWords := []string{"ahoy", "matey", "arr", "ye", "aye"}
	hasPirateWord := false
	responseLower := strings.ToLower(response)
	for _, word := range pirateWords {
		if strings.Contains(responseLower, word) {
			hasPirateWord = true
			break
		}
	}
	if !hasPirateWord {
		t.Log("Warning: Response may not follow pirate system prompt")
	}
}

// TestConversationResume verifies we can continue a session with tool results
func TestConversationResume(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First turn: Ask claude to read a file
	cmd1 := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--tools", "Read",
		"--", "Read /etc/hosts")

	stdout1, _ := cmd1.StdoutPipe()
	cmd1.Start()

	scanner1 := bufio.NewScanner(stdout1)
	var sessionID string
	var toolCallID string
	var toolName string

	for scanner1.Scan() {
		line := scanner1.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(line), &baseMsg)

		if baseMsg.Type == "system" {
			var msg SystemMessage
			json.Unmarshal([]byte(line), &msg)
			sessionID = msg.SessionID
			t.Logf("Turn 1 - Session: %s", sessionID)
		}
		if baseMsg.Type == "assistant" {
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "tool_use" {
					toolCallID = c.ID
					toolName = c.Name
					t.Logf("Turn 1 - Tool call: %s (id=%s)", c.Name, c.ID)
				}
			}
		}
	}
	cmd1.Wait()

	if sessionID == "" {
		t.Fatal("No session ID from first turn")
	}
	if toolCallID == "" {
		t.Fatal("No tool call from first turn")
	}

	// Second turn: Resume session with tool result
	// Using --resume to continue the conversation
	cmd2 := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "1",
		"--resume", sessionID,
		"--", fmt.Sprintf("Tool result for %s (id=%s): Contents of /etc/hosts: localhost 127.0.0.1", toolName, toolCallID))

	stdout2, _ := cmd2.StdoutPipe()
	stderr2, _ := cmd2.StderrPipe()
	cmd2.Start()

	// Read stderr
	go func() {
		scanner := bufio.NewScanner(stderr2)
		for scanner.Scan() {
			t.Logf("stderr: %s", scanner.Text())
		}
	}()

	scanner2 := bufio.NewScanner(stdout2)
	var gotResponse bool

	for scanner2.Scan() {
		line := scanner2.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(line), &baseMsg)

		if baseMsg.Type == "assistant" {
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "text" && c.Text != "" {
					gotResponse = true
					t.Logf("Turn 2 - Response: %s", truncate(c.Text, 100))
				}
			}
		}
	}
	cmd2.Wait()

	if !gotResponse {
		t.Error("No response in second turn")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestNativeWebSearch verifies web search works
func TestNativeWebSearch(t *testing.T) {
	if os.Getenv("TEST_CLAUDE_BIN") == "" {
		t.Skip("Set TEST_CLAUDE_BIN=1 to run claude binary tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Include WebSearch tool
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "haiku",
		"--max-turns", "3", // Allow multiple turns for search
		"--tools", "WebSearch",
		"--", "What is the current weather in San Francisco? Search the web for this.")

	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	scanner := bufio.NewScanner(stdout)
	var gotWebSearch bool

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		json.Unmarshal([]byte(line), &baseMsg)

		if baseMsg.Type == "assistant" {
			var msg AssistantMessage
			json.Unmarshal([]byte(line), &msg)
			for _, c := range msg.Message.Content {
				if c.Type == "tool_use" && c.Name == "WebSearch" {
					gotWebSearch = true
					t.Logf("WebSearch called with: %s", string(c.Input))
				}
				if c.Type == "text" && c.Text != "" {
					t.Logf("Response: %s", truncate(c.Text, 150))
				}
			}
		}
	}
	cmd.Wait()

	if !gotWebSearch {
		t.Log("Note: WebSearch may not have been called if model answered from knowledge")
	}
}

func TestMain(m *testing.M) {
	// Check if claude binary exists
	_, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("claude binary not found in PATH")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
