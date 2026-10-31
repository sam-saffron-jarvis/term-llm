package clibin

import (
	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/mcphttp"
)

// forcedClaudeCodeIsolationEnv disables Claude Code surfaces that can inject
// first-party prompt/tool behavior into claude-bin runs. term-llm owns tools,
// memory, background jobs, and project instructions for this provider; Claude
// Code is only the authenticated model transport plus MCP client.
var forcedClaudeCodeIsolationEnv = map[string]string{
	"CLAUDE_CODE_DISABLE_WORKFLOWS":            "1",
	"DISABLE_GROWTHBOOK":                       "1",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	"CLAUDE_CODE_DISABLE_AGENT_VIEW":           "1",
	"CLAUDE_CODE_DISABLE_CRON":                 "1",
	"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":     "1",
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY":          "1",
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS":           "1",
	"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS":     "1",
	"CLAUDE_CODE_DISABLE_ADVISOR_TOOL":         "1",
	"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS":   "1",
	// Tool search defers MCP tools until the model searches for them. Every
	// term-llm tool is an MCP tool, so it must never hide the catalogue.
	"ENABLE_TOOL_SEARCH": "false",
}

// ignoredInheritedClaudeCodeEnv lists shell variables aimed at interactive
// Claude Code that silently break or override claude-bin. Values set
// explicitly in the claude-bin provider env are still honoured.
//   - CLAUDE_CODE_SIMPLE skips OAuth, so subscription logins report "Not
//     logged in" even with CLAUDE_CODE_OAUTH_TOKEN set.
//   - CLAUDE_CODE_EFFORT_LEVEL would override term-llm's effort selection.
//   - MAX_MCP_OUTPUT_TOKENS would replace claudeCodeDefaultEnv's raised cap.
var ignoredInheritedClaudeCodeEnv = map[string]bool{
	"CLAUDE_CODE_SIMPLE":       true,
	"CLAUDE_CODE_EFFORT_LEVEL": true,
	"MAX_MCP_OUTPUT_TOKENS":    true,
}

// claudeCodeDefaultEnv is applied unless the provider env sets the key.
// term-llm truncates tool output itself (tools.max_tool_output_chars, default
// 20000 chars), so Claude Code's own MCP output cap should sit above it. These
// fixed limits leave wide headroom but do not track arbitrarily raised
// max_tool_output_chars values. When Claude Code's cap trips,
// Claude Code replaces the result with "saved to a file, read it with Read",
// and claude-bin runs with Read disabled, so the model cannot recover.
var claudeCodeDefaultEnv = map[string]string{
	"MAX_MCP_OUTPUT_TOKENS": "200000",
}

// claudeMCPMaxResultSizeChars is published per tool as
// _meta["anthropic/maxResultSizeChars"]. Claude Code otherwise persists any
// result above ~50k characters to disk and shows the model a 2KB preview,
// which claude-bin's model cannot read past without the built-in Read tool.
const claudeMCPMaxResultSizeChars = 500_000

// claudeMCPToolSpecs adds Claude Code's per-tool hints to the shared bridge
// specs: never defer the tool behind tool search, and let results up to
// claudeMCPMaxResultSizeChars through inline.
func claudeMCPToolSpecs(tools []core.ToolSpec) []mcphttp.ToolSpec {
	specs := mcpToolSpecs(tools)
	for i := range specs {
		specs[i].Meta = map[string]any{
			"anthropic/alwaysLoad":         true,
			"anthropic/maxResultSizeChars": claudeMCPMaxResultSizeChars,
		}
	}
	return specs
}
