---
title: "Session management"
weight: 5
description: "List, search, resume, export, and prune local sessions stored in SQLite."
kicker: "State"
source_readme_heading: "Session Management"
featured: true
---
Chat sessions are automatically stored locally and can be managed with the `sessions` command. Each session is assigned a sequential number (#1, #2, #3...) for easy reference:

```bash
term-llm sessions                        # List recent sessions
term-llm sessions list --provider anthropic  # Filter by provider
term-llm sessions search "kubernetes"    # Search session content
term-llm sessions show 42                # Show session details (by number)
term-llm sessions show #42               # Same thing (explicit # prefix)
term-llm sessions export 42 [path.md]    # Export as markdown
term-llm sessions name 42 "my session"   # Set custom name
term-llm sessions delete 42              # Delete a session
term-llm sessions reset                  # Delete all sessions
term-llm chat --resume=42                # Resume a session by number
```

Sessions are stored in a SQLite database at `~/.local/share/term-llm/sessions.db`. Configure session storage in your config:

```yaml
sessions:
  enabled: true       # Master switch (default: true)
  max_age_days: 0     # Auto-delete sessions older than N days (0=never)
  max_count: 0        # Keep at most N sessions (0=unlimited)
  path: ""            # Optional DB path override (supports :memory:)
```

Use CLI overrides when needed:

```bash
term-llm chat --no-session                      # Do not read/write session DB
term-llm ask --session-db /tmp/term-llm.db ... # Use an alternate DB path
```

### Conversation Inspector

While in `chat` or `ask` mode, press `Ctrl+O` to open the conversation inspector. This shows the full conversation history including tool calls and results, with vim-style navigation:

| Key | Action |
|-----|--------|
| `j/k` | Scroll up/down |
| `g/G` | Go to top/bottom |
| `e` | Toggle expand/collapse |
| `q` | Close inspector |

The main config lives at `~/.config/term-llm/config.yaml`: 

```yaml
default_provider: anthropic

providers:
  # Built-in providers - type is inferred from the key name
  anthropic:
    model: claude-sonnet-4-6

  openai:
    model: gpt-5.2
    credentials: codex  # or "api_key" (default)

  xai:
    model: grok-4-1-fast  # grok-4, grok-3, grok-code-fast-1

  openrouter:
    model: x-ai/grok-code-fast-1
    app_url: https://github.com/samsaffron/term-llm
    app_title: term-llm

  gemini:
    model: gemini-3-flash-preview  # uses GEMINI_API_KEY

  gemini-cli:
    model: gemini-3-flash-preview  # uses ~/.gemini/oauth_creds.json

  zen:
    model: glm-4.7-free
    # api_key is optional - leave empty for free tier

  copilot:
    model: gpt-4.1  # free tier, or gpt-5.2-codex for paid

  # Local LLM providers (require explicit type)
  # Run 'term-llm models --provider ollama' to list available models
  # ollama:
  #   type: openai_compatible
  #   base_url: http://localhost:11434/v1
  #   model: llama3.2:latest

  # Custom OpenAI-compatible endpoints
  # cerebras:
  #   type: openai_compatible
  #   base_url: https://api.cerebras.ai/v1  # /chat/completions appended automatically
  #   # url: https://api.cerebras.ai/v1/chat/completions  # alternative: full URL, used as-is
  #   model: llama-4-scout-17b
  #   api_key: ${CEREBRAS_API_KEY}
  #   models:  # optional: enable autocomplete for --provider cerebras:<TAB>
  #     - llama-4-scout-17b-16e-instruct
  #     - llama-4-maverick-17b-128e-instruct
  #     - qwen-3-32b

exec:
  suggestions: 3  # number of command suggestions
  # provider: openai    # override provider for exec only
  # model: gpt-4o       # override model for exec only
  instructions: |
    I use Arch Linux with zsh.
    I prefer ripgrep over grep, fd over find.

ask:
  # provider: anthropic
  # model: claude-opus-4  # use a smarter model for questions
  instructions: |
    Be concise. I'm an experienced developer.

edit:
  # provider: openai
  # model: gpt-5.2-codex  # Codex models are optimized for code edits
  diff_format: auto  # auto, udiff, or replace

image:
  provider: gemini  # gemini, openai, xai, venice, flux, or openrouter
  output_dir: ~/Pictures/term-llm

  gemini:
    api_key: ${GEMINI_API_KEY}
    # model: gemini-2.5-flash-image

  openai:
    api_key: ${OPENAI_API_KEY}
    # model: gpt-image-1

  xai:
    api_key: ${XAI_API_KEY}
    # model: grok-2-image-1212

  venice:
    api_key: ${VENICE_API_KEY}
    # model: nano-banana-pro
    # resolution: 2K

  flux:
    api_key: ${BFL_API_KEY}
    # model: flux-2-pro

  openrouter:
    api_key: ${OPENROUTER_API_KEY}
    # model: google/gemini-2.5-flash-image

embed:
  # provider: gemini  # gemini, openai, jina, voyage, ollama (auto-detected from default_provider)

  # gemini:
  #   api_key: ${GEMINI_API_KEY}
  #   model: gemini-embedding-001

  # openai:
  #   api_key: ${OPENAI_API_KEY}
  #   model: text-embedding-3-small

  # jina:
  #   api_key: ${JINA_API_KEY}        # free tier: 10M tokens at jina.ai/embeddings
  #   model: jina-embeddings-v3

  # voyage:
  #   api_key: ${VOYAGE_API_KEY}
  #   model: voyage-3.5

  # ollama:
  #   base_url: http://localhost:11434
  #   model: nomic-embed-text

search:
  provider: duckduckgo  # exa, brave, google, or duckduckgo (default)

  # exa:
  #   api_key: ${EXA_API_KEY}

  # brave:
  #   api_key: ${BRAVE_API_KEY}

  # google:
  #   api_key: ${GOOGLE_SEARCH_API_KEY}
  #   cx: ${GOOGLE_SEARCH_CX}

tools:
  max_tool_output_chars: 20000  # truncate tool outputs before sending to LLM (default 20000, 0 to disable)
```

### Per-Command Provider/Model

Each command (exec, ask, edit) can have its own provider and model, overriding the global default:

```yaml
default_provider: anthropic  # global default

providers:
  anthropic:
    model: claude-sonnet-4-6
  openai:
    model: gpt-5.2
  zen:
    model: glm-4.7-free

exec:
  provider: zen       # exec uses Zen (free)
  model: glm-4.7-free

ask:
  model: claude-opus-4  # ask uses global provider with a smarter model

edit:
  provider: openai
  model: gpt-5.2       # edit uses OpenAI
```

**Precedence** (highest to lowest):
1. CLI flag: `--provider openai:gpt-5.2`
2. Per-command config: `exec.provider` / `exec.model`
3. Global config: `default_provider` + `providers.<name>.model`

### Reasoning Effort (OpenAI)

For OpenAI models, you can control reasoning effort by appending `-low`, `-medium`, `-high`, or `-xhigh` to the model name:

```bash
term-llm ask --provider openai:gpt-5.2-xhigh "complex question"  # max reasoning
term-llm exec --provider openai:gpt-5.2-low "quick task"         # faster/cheaper
```

Or in config:
```yaml
providers:
  openai:
    model: gpt-5.2-high  # effort parsed from suffix
```

| Effort | Description |
|--------|-------------|
| `low` | Faster, cheaper, less thorough |
| `medium` | Balanced (default if not specified) |
| `high` | More thorough reasoning |
| `xhigh` | Maximum reasoning (only on gpt-5.2) |

### Extended Thinking (Anthropic)

For Anthropic models, you can enable extended thinking by appending `-thinking` to the model name:

```bash
term-llm ask --provider anthropic:claude-sonnet-4-6-thinking "complex question"
```

Or in config:
```yaml
providers:
  anthropic:
    model: claude-sonnet-4-6-thinking  # enables 10k token thinking budget
```

Extended thinking allows Claude to reason through complex problems before responding. The thinking process uses ~10,000 tokens and is not shown in the output.

### Web Search

When using `-s`/`--search`, some providers (Anthropic, OpenAI, xAI, Gemini) have native web search built-in. xAI also includes X (Twitter) search. Others use external tools (configurable search provider + [Jina Reader](https://jina.ai/reader/)).

You can force external search even for providers with native support—useful for consistency, debugging, or when native search doesn't work well for your use case.

**CLI flags:**
```bash
term-llm ask "latest news" -s --no-native-search  # Force external search tools
term-llm ask "latest news" -s --native-search     # Force native (override config)
```

**Global config** (applies to all providers):
```yaml
search:
  force_external: true  # Never use native search, always use external tools
```

**Per-provider config:**
```yaml
providers:
  gemini:
    model: gemini-2.5-flash
    use_native_search: false  # Always use external search for this provider

  anthropic:
    model: claude-sonnet-4-6
    # use_native_search: true  # Default: use native if available
```

**Priority** (highest to lowest):
1. CLI flag: `--native-search` or `--no-native-search`
2. Global config: `search.force_external: true`
3. Provider config: `use_native_search: false`
4. Default: use native search if provider supports it

### Search Providers

When using external search (non-native), you can choose from multiple search providers:

| Provider | Environment Variable | Description |
|----------|---------------------|-------------|
| DuckDuckGo (default) | — | Free, no API key required |
| [Exa](https://exa.ai) | `EXA_API_KEY` | AI-native semantic search |
| [Tavily](https://tavily.com/) | `TAVILY_API_KEY` | Agent-oriented web search with snippets |
| [Brave](https://brave.com/search/api/) | `BRAVE_API_KEY` | Independent index, privacy-focused |
| [Google](https://developers.google.com/custom-search) | `GOOGLE_SEARCH_API_KEY` + `GOOGLE_SEARCH_CX` | Google Custom Search |

**Configure in `~/.config/term-llm/config.yaml`:**
```yaml
search:
  provider: exa  # exa, tavily, brave, google, or duckduckgo (default)

  exa:
    api_key: ${EXA_API_KEY}

  tavily:
    api_key: ${TAVILY_API_KEY}

  brave:
    api_key: ${BRAVE_API_KEY}

  google:
    api_key: ${GOOGLE_SEARCH_API_KEY}
    cx: ${GOOGLE_SEARCH_CX}  # Custom Search Engine ID
```

Run `term-llm config` to see which search providers have credentials configured.

### Credentials

Most providers use API keys via environment variables. Some providers use OAuth credentials from companion CLIs:

| Provider | Credentials Source | Description |
|----------|-------------------|-------------|
| `anthropic` | `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, or OAuth | Anthropic API key or OAuth token |
| `openai` | `OPENAI_API_KEY` | OpenAI API key |
| `chatgpt` | `~/.config/term-llm/chatgpt_creds.json` | ChatGPT Plus/Pro OAuth |
| `copilot` | `~/.config/term-llm/copilot_creds.json` | GitHub Copilot OAuth |
| `gemini` | `GEMINI_API_KEY` | Google AI Studio API key |
| `gemini-cli` | `~/.gemini/oauth_creds.json` | gemini-cli OAuth (Google Code Assist) |
| `xai` | `XAI_API_KEY` | xAI API key |
| `openrouter` | `OPENROUTER_API_KEY` | OpenRouter API key |
| `zen` | `ZEN_API_KEY` (optional) | Empty for free tier |

**Anthropic**, **ChatGPT**, **Copilot**, and **Gemini CLI** work without any API key if you have a subscription or the CLI installed and logged in:

```bash
term-llm ask --provider anthropic "question"  # uses OAuth token (runs `claude setup-token` on first use)
term-llm ask --provider chatgpt "question"    # uses ChatGPT Plus/Pro subscription
term-llm ask --provider copilot "question"    # uses GitHub Copilot OAuth
term-llm ask --provider gemini-cli "question" # uses ~/.gemini/oauth_creds.json
```

### Dynamic Configuration

For advanced setups, term-llm supports dynamic resolution of API keys and URLs using special prefixes. These are resolved lazily—only when actually making an API call, not when loading config.

#### 1Password Integration (`op://`)

Retrieve API keys from 1Password using secret references:

```yaml
providers:
  my-provider:
    type: openai_compatible
    base_url: https://api.example.com/v1
    api_key: "op://Private/My API Key/credential"
```

For multiple 1Password accounts, use the `?account=` query parameter:

```yaml
providers:
  work-llm:
    type: openai_compatible
    base_url: https://llm.company.com/v1
    api_key: "op://Engineering/LLM Service/api_key?account=company.1password.com"
```

This requires the [1Password CLI](https://developer.1password.com/docs/cli/) (`op`) to be installed and signed in.

#### DNS SRV Records (`srv://`)

Discover server endpoints dynamically via DNS SRV records:

```yaml
providers:
  internal-llm:
    type: openai_compatible
    url: "srv://_llm._tcp.internal.company.com/v1/chat/completions"
    api_key: ${LLM_API_KEY}
```

The SRV record is resolved to `https://host:port/path`. This is useful for:
- Load-balanced services with multiple backends
- Internal services with dynamic IPs
- Kubernetes services exposed via external-dns

#### Shell Commands (`$()`)

Execute arbitrary shell commands to get values:

```yaml
providers:
  vault-backed:
    type: openai_compatible
    base_url: https://api.example.com/v1
    api_key: "$(vault kv get -field=api_key secret/llm)"

  aws-secrets:
    type: openai_compatible
    base_url: https://api.example.com/v1
    api_key: "$(aws secretsmanager get-secret-value --secret-id llm-key --query SecretString --output text)"
```

#### Combined Example

Using SRV discovery with 1Password credentials:

```yaml
providers:
  production-llm:
    type: openai_compatible
    model: "Qwen/Qwen3-30B-A3B"
    url: "srv://_vllm._tcp.ml.company.com/v1/chat/completions"
    api_key: "op://Infrastructure/vLLM Cluster/credential?account=company.1password.com"
```

When you run `term-llm config`, these show as `[set via 1password]` or `[set via command]` without actually resolving the values (no 1Password prompt until you make an API call).

### Diagnostics

Enable diagnostic logging to capture detailed information when edits fail and retry. This is useful for debugging and tuning prompts:

```yaml
diagnostics:
  enabled: true
  # dir: /custom/path  # optional, defaults to ~/.local/share/term-llm/diagnostics/
```

When an edit fails and retries, two files are written:
- `edit-retry-{timestamp}.json` - Structured data for programmatic analysis
- `edit-retry-{timestamp}.md` - Human-readable with syntax-highlighted code blocks

Each diagnostic captures:
- Provider and model used
- Full system and user prompts
- LLM's partial response before failure
- Failed search pattern or diff
- Current file content
- Error reason

### Shell Completions

Generate and install shell completions:

```bash
term-llm config completion zsh --install   # Install for zsh
term-llm config completion bash --install  # Install for bash
term-llm config completion fish --install  # Install for fish
```
