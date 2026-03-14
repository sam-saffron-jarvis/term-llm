---
title: "Plan mode"
weight: 12
description: "Use the planning TUI to co-edit a plan document with an AI agent in real time."
kicker: "Planning"
next:
  label: Notifications
  url: /guides/notifications/
---
## Start a planning session

```bash
term-llm plan
term-llm plan project.md
```

Plan mode opens a collaborative planning TUI where you and the model edit a plan document together.

## Useful flags

```bash
term-llm plan --provider chatgpt
term-llm plan --no-search
term-llm plan --max-turns 50
term-llm plan --file roadmap.md
```

## Keyboard shortcuts

- `Ctrl+P` invoke the planner agent
- `Ctrl+S` save the document
- `Ctrl+C` cancel the agent or quit
- `Esc` leave insert mode

## What the planner can do

The planner agent can:

- add structure such as headings and sections
- reorganize material
- ask clarifying questions
- make incremental edits while preserving your changes

Plan mode also wires read-only investigation tools and limited sub-agent delegation for deeper exploration.

## When to use it

Use plan mode when you want to turn rough notes into:

- project plans
- research outlines
- implementation checklists
- decision documents

## Related pages

- [Usage](/guides/usage/)
- [Agents](/guides/agents/)
- [Search](/guides/search/)
