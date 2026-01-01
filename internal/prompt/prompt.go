package prompt

import (
	"fmt"
	"os"
	"runtime"
)

// SystemPrompt returns the system prompt for command suggestions
func SystemPrompt(shell string, customContext string, enableSearch bool) string {
	cwd, _ := os.Getwd()
	base := fmt.Sprintf(`You are a CLI command expert. The user will describe what they want to do, and you must suggest exactly 3 shell commands that accomplish their goal.

Context:
- Operating System: %s
- Architecture: %s
- Shell: %s
- Current Directory: %s`, runtime.GOOS, runtime.GOARCH, shell, cwd)

	if customContext != "" {
		base += fmt.Sprintf(`
- User Context: %s`, customContext)
	}

	base += `

Rules:
1. Suggest exactly 3 different commands or approaches
2. Each command should be a valid, executable shell command
3. Prefer common tools that are likely to be installed
4. Order suggestions from most likely to be useful to least
5. Keep explanations brief (under 50 words)
6. If the request is dangerous (rm -rf /, etc), still provide the command but warn in the explanation`

	if enableSearch {
		base += `
7. You have access to web search. Use it to find current documentation, latest versions, or up-to-date syntax when relevant
8. After searching (if needed), call the suggest_commands tool with your suggestions`
	}

	return base
}

// UserPrompt formats the user's request
func UserPrompt(userInput string) string {
	return fmt.Sprintf("I want to: %s", userInput)
}

// CommandHelpPrompt returns the prompt for generating detailed command help
func CommandHelpPrompt(command, shell string) string {
	return fmt.Sprintf(`You are a friendly CLI tutor. Explain the following %s command in detail. Be comprehensive, educational, and memorable.

Command: %s

Please cover these sections:

## What It Does
Explain the command's purpose and what it accomplishes in plain language.

## Breaking Down the Command
Explain each part of the command: the base command, every flag, every argument, pipes, redirections, etc. Use a bullet point for each component.

## Common Flags & Options
List 4-6 other commonly used flags for this command that the user might find useful.

## Memorization Tips
Provide mnemonics, patterns, or memory tricks to help remember the syntax. Be creative and memorable!

## History & Background
Brief background on where this command came from (Unix history, GNU coreutils, original author, when it was created, etc.).

## Related Commands
List 2-3 related commands the user might find useful, with a one-line description of each.

## More Examples
Show 2-3 additional practical examples of this command for different use cases.

Format your response in clear, readable markdown. Be concise but thorough.`, shell, command)
}

// JSONSchema returns the JSON schema for structured output
func JSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"suggestions": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The shell command to execute",
						},
						"explanation": map[string]interface{}{
							"type":        "string",
							"description": "Brief explanation of what the command does",
						},
						"likelihood": map[string]interface{}{
							"type":        "integer",
							"description": "How likely this command matches user intent (1=unlikely, 10=very likely)",
						},
					},
					"required": []string{"command", "explanation", "likelihood"},
				},
				"minItems": 3,
				"maxItems": 3,
			},
		},
		"required": []string{"suggestions"},
	}
}
