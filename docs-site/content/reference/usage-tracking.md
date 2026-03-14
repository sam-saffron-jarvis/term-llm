---
title: "Usage tracking"
weight: 4
description: "Inspect token usage and local cost data for term-llm and related CLI tools."
kicker: "Accounting"
source_readme_heading: "Usage Tracking"
next:
  label: Session management
  url: /reference/sessions/
---
View token usage and costs from local CLI tools:

```bash
term-llm usage                           # Show all usage
term-llm usage --provider claude-code    # Filter by provider
term-llm usage --provider term-llm       # term-llm usage only
term-llm usage --since 20250101          # From specific date
term-llm usage --breakdown               # Per-model breakdown
term-llm usage --json                    # JSON output
```

Supported sources: Claude Code, Gemini CLI, and term-llm's own usage logs.
