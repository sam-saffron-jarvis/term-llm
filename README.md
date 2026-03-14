<p align="center">
  <img src="assets/logo.png" alt="term-llm logo" width="200">
</p>

# term-llm

A Swiss Army knife for your terminal—AI-powered commands, answers, and images at your fingertips.

[![Release](https://img.shields.io/github/v/release/samsaffron/term-llm?style=flat-square)](https://github.com/samsaffron/term-llm/releases)

Docs hub: **https://term-llm.com**

## Features

- **Command suggestions**: Natural language → executable shell commands
- **Ask questions**: Get answers with optional web search
- **Chat mode**: Persistent sessions with tool and MCP support
- **File editing**: Edit code with AI assistance (supports line ranges)
- **Image generation**: Create and edit images (Gemini, OpenAI, xAI, Flux)
- **Text embeddings**: Generate vector embeddings for search, RAG, and similarity
- **Agents and skills**: Named workflow bundles and portable expertise
- **Multiple providers**: Anthropic, OpenAI, ChatGPT, GitHub Copilot, xAI, OpenRouter, Gemini, Ollama, LM Studio, and more

```bash
$ term-llm exec "find all go files modified today"

> find . -name "*.go" -mtime 0   Uses find with name pattern
  fd -e go --changed-within 1d   Uses fd (faster alternative)
  find . -name "*.go" -newermt "today"   Alternative find syntax
  something else...
```

## Installation

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/samsaffron/term-llm/main/install.sh | sh
```

### Go install

```bash
go install github.com/samsaffron/term-llm@latest
```

## Quickstart

Use the free Zen provider to get a first run without API-key setup:

```bash
term-llm exec --provider zen "list files"
term-llm ask --provider zen "explain git rebase"
term-llm chat
```

If you already have a provider key:

```bash
export ANTHROPIC_API_KEY=your-key
# or OPENAI_API_KEY / GEMINI_API_KEY / OPENROUTER_API_KEY / XAI_API_KEY
```

## Documentation

The detailed docs now live on **https://term-llm.com** and are maintained as Markdown in this repo, built into the site with Hugo.

- [Getting started](https://term-llm.com/getting-started/)
- [Guides](https://term-llm.com/guides/)
- [Architecture](https://term-llm.com/architecture/)
- [Reference](https://term-llm.com/reference/)
- [Configuration](https://term-llm.com/reference/configuration/)
- [Agents](https://term-llm.com/guides/agents/)
- [Skills](https://term-llm.com/guides/skills/)
- [MCP servers](https://term-llm.com/guides/mcp-servers/)

## License

MIT
