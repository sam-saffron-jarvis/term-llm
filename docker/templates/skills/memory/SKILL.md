---
name: memory
description: "Read and update persistent memory. Use when: user says 'remember', asks about past context, preferences, or projects. Also use proactively to save anything reusable before a conversation ends."
---

# Memory System

Memory is stored as **fragments** in a SQLite database, indexed with BM25 + vector embeddings.
Mining runs every 30 min; GC runs daily. The `recent.md` file is auto-promoted from fragments.

## Lookup — always search first

```bash
term-llm memory search "<query>" --agent "$AGENT_NAME" --limit 6
```

Hybrid BM25 + vector search — use it for anything that might be in memory.

Show full fragment by numeric ID (preferred — no truncation):

```bash
term-llm memory fragments show 42 --agent "$AGENT_NAME"
```

List and filter:

```bash
term-llm memory fragments list --agent "$AGENT_NAME" --limit 10
term-llm memory fragments list --agent "$AGENT_NAME" --filter-path telegram
```

## Writing new memory

Use the CLI to add fragments directly to the database:

```bash
term-llm memory fragments add fragments/<category>/<name>.md --agent "$AGENT_NAME" --content "..."
term-llm memory fragments update fragments/<category>/<name>.md --agent "$AGENT_NAME" --content "..."
term-llm memory fragments delete fragments/<category>/<name>.md --agent "$AGENT_NAME"
```

Category guide:
- `preferences/` — user stated preferences
- `projects/` — project context, decisions
- `notes/` — one-off facts
- `tools/` — CLI tools, APIs, snippets

## When user says "remember this"

1. Add the fragment immediately via CLI
2. Confirm to user

## Memory rules

- **Search before answering** anything about user preferences, history, or projects
- `recent.md` is loaded at session start (auto-promoted from fragments) — never edit it directly
- The session miner handles most things automatically after conversations end
- Proactively create fragments when content is structured or unlikely to survive miner summarisation

## Job schedule (created by bootstrap-jobs)

| Job | Schedule | Purpose |
|-----|----------|---------|
| `mine-sessions` | every 30 min | Extract fragments from transcripts |
| `update-recent` | every 10 min | Promote fragments into recent.md |
| `memory-gc` | daily 4am | Garbage-collect stale fragments |
