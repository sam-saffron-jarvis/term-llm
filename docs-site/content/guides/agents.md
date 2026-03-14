---
title: "Agents"
weight: 9
description: "Create and manage named workflow bundles with their own provider, model, tools, and instructions."
kicker: "Workflow bundles"
source_readme_heading: "Agents"
featured: true
next:
  label: Skills
  url: /guides/skills/
---
Agents are named configuration bundles that define a persona with specific provider, model, system prompt, tools, and MCP servers. Use agents to switch between different workflows quickly.

### Using Agents

Use the `@agent` prefix syntax or `--agent` flag:

```bash
term-llm ask @reviewer "review this code"     # use reviewer agent
term-llm chat @coder                          # start chat with coder agent
term-llm ask --agent reviewer "question"      # alternative syntax
```

### Managing Agents

```bash
term-llm agents                              # List all agents
term-llm agents list                         # Same as above
term-llm agents list --builtin               # Only built-in agents
term-llm agents list --local                 # Only local agents
term-llm agents list --user                  # Only user agents
term-llm agents new my-agent                 # Create new agent
term-llm agents show reviewer                # Show agent configuration
term-llm agents edit reviewer                # Edit agent configuration
term-llm agents copy builtin/coder my-coder  # Copy agent to customize
term-llm agents path                         # Print agents directory
```

### Agent Configuration

Agents are YAML files stored in `~/.config/term-llm/agents/`:

```yaml
# ~/.config/term-llm/agents/reviewer/agent.yaml
name: Code Reviewer
description: Reviews code for best practices and potential issues

provider: anthropic
model: claude-sonnet-4-6

tools:
  enabled: [read_file, grep, glob]
  # OR use a denylist instead:
  # disabled: [shell, write_file]

shell:
  allow: ["git *", "npm test"]  # glob patterns for allowed commands
  auto_run: true                 # skip confirmation for matched commands
  scripts:                       # named shortcuts (auto-approved)
    build: "npm run build"

search: true   # enables web_search and read_url tools
max_turns: 50

mcp:
  - name: github
```

Built-in agents that currently default to `search: true`: `agent-builder`, `researcher`, `developer`, `editor`, `shell`.

### System Prompt File Includes

System prompts support inline file includes with `{{file:...}}`.

```md
You are a reviewer.

{{file:prompts/rules.md}}
{{file:/absolute/path/to/shared-context.md}}
```

Behavior:
- Includes are expanded recursively (max depth: 10)
- Cycles are detected and reported as errors
- Missing/unreadable include files fail fast
- Included content is inserted raw (no automatic headers/separators)
- Relative paths are source-relative:
  - Agent prompts resolve relative to the agent directory
  - Config/CLI system prompts resolve relative to the current working directory
- Expansion order is include first, then template variables (for example `{{year}}`)

**Agent search order:** user agents → local agents → built-in agents
