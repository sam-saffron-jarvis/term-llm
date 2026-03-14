---
title: "Skills"
weight: 10
description: "Use and manage portable instruction bundles that add task-specific context."
kicker: "Portable expertise"
source_readme_heading: "Skills"
featured: true
next:
  label: Built-in tools
  url: /reference/built-in-tools/
---
Skills are portable instruction bundles that provide specialized knowledge for specific tasks. Unlike agents, skills don't change the provider or model—they just add context.

### Using Skills

```bash
term-llm ask --skills git "how to squash commits"   # use git skill
term-llm chat --skills git,docker                   # multiple skills
term-llm edit --skills refactoring -f main.go "refactor this"
```

### Managing Skills

```bash
term-llm skills                              # List all skills
term-llm skills list                         # Same as above
term-llm skills list --local                 # Only local skills
term-llm skills list --user                  # Only user skills
term-llm skills new my-skill                 # Create new skill
term-llm skills show git                     # Show skill content
term-llm skills edit git                     # Edit skill
term-llm skills copy builtin/git my-git      # Copy skill to customize
term-llm skills browse                       # Browse available skills
term-llm skills validate my-skill            # Validate skill syntax
term-llm skills update                       # Update skills from sources
term-llm skills path                         # Print skills directory
term-llm skills add owner/repo               # Add skill source from GitHub
```

### Skill Configuration

Skills live in `~/.config/term-llm/skills/<name>/SKILL.md`. Each skill is a directory containing a `SKILL.md` file with YAML frontmatter and Markdown body:

```markdown
---
name: git
description: "Git version control expertise"
---

# Git Skill

When helping with Git:
- Prefer rebase over merge for cleaner history
- Use conventional commit messages
- Explain the implications of destructive operations
```

**Skill search order:** local (project) → user (`~/.config/term-llm/skills/`) → built-in

### Skill Tools

Skills can declare script-backed tools in their frontmatter. When the skill is activated via `activate_skill`, those tools are dynamically registered with the engine—the LLM can then call them directly, with no hardcoded paths anywhere.

```markdown
---
name: google-maps
description: "Google Maps queries: travel times, place search, geocoding"
tools:
  - name: maps_travel_time
    description: "Get traffic-aware travel time between two locations"
    script: scripts/travel-time.sh
    timeout_seconds: 15
    input:
      type: object
      properties:
        origin:
          type: string
          description: "Origin address or lat,lng"
        destination:
          type: string
          description: "Destination address or lat,lng"
        mode:
          type: string
          description: "DRIVE, WALK, BICYCLE, or TRANSIT (default: DRIVE)"
      required: [origin, destination]

  - name: maps_places_search
    description: "Free-text place search with optional location bias"
    script: scripts/places-search.sh
    input:
      type: object
      properties:
        query:
          type: string
        latlng:
          type: string
          description: "Optional bias point as lat,lng"
      required: [query]
---

# Google Maps Skill

API key is embedded in the scripts—no need to handle it here.
...
```

Scripts live in the skill directory (e.g. `scripts/travel-time.sh`) and receive the LLM's arguments as **JSON on stdin**, exactly like agent custom tools:

```bash
#!/usr/bin/env bash
INPUT=$(cat)
ORIGIN=$(echo "$INPUT" | jq -r '.origin')
DESTINATION=$(echo "$INPUT" | jq -r '.destination')
MODE=$(echo "$INPUT" | jq -r '.mode // "DRIVE"')
# ... call the API
```

This is the recommended pattern for skills that need API keys or other secrets—the key lives only in the script, never in the SKILL.md body that gets injected into the LLM context.

**Field reference** (same as agent custom tools):

| Field | Required | Description |
|-------|----------|-------------|
| `name` | ✓ | Tool name shown to LLM. Must match `^[a-z][a-z0-9_]*$` |
| `description` | ✓ | Description passed to LLM in the tool spec |
| `script` | ✓ | Path relative to the skill directory (e.g. `scripts/foo.sh`) |
| `input` | | JSON Schema for parameters. Must be `type: object` at root |
| `timeout_seconds` | | Execution timeout (default 30, max 300) |
| `env` | | Extra environment variables when running the script |

Scripts run with `TERM_LLM_AGENT_DIR` set to the skill's directory and `TERM_LLM_TOOL_NAME` set to the tool name. Symlinks are resolved and containment-checked—scripts cannot escape the skill directory.
