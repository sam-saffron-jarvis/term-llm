---
title: "Search"
weight: 6
description: "Use web search in term-llm, choose external providers, and control native versus external search routing."
kicker: "Web search"
featured: true
next:
  label: Jobs
  url: /guides/job-runner/
---
## Search modes

When you use `-s` or `--search`, term-llm can answer with either:

- **native provider search** when the model backend supports it
- **external search tools** using the configured search provider plus page reading

Examples:

```bash
term-llm ask "latest node.js version" -s
term-llm exec "install latest docker" -s
```

## Force native or external behavior

```bash
term-llm ask "latest news" -s --native-search
term-llm ask "latest news" -s --no-native-search
```

Use this when you want consistency, debugging clarity, or to work around a provider’s native search behavior.

## Configure external search

```yaml
search:
  provider: exa

  exa:
    api_key: ${EXA_API_KEY}

  brave:
    api_key: ${BRAVE_API_KEY}

  google:
    api_key: ${GOOGLE_SEARCH_API_KEY}
    cx: ${GOOGLE_SEARCH_CX}
```

Available external providers:

| Provider | Credentials | Notes |
|---|---|---|
| DuckDuckGo | none | default, free |
| Exa | `EXA_API_KEY` | semantic search |
| Tavily | `TAVILY_API_KEY` | agent-oriented search |
| Brave | `BRAVE_API_KEY` | independent index |
| Google | `GOOGLE_SEARCH_API_KEY` + `GOOGLE_SEARCH_CX` | Custom Search |

## Native versus external priority

Priority is:

1. CLI override: `--native-search` or `--no-native-search`
2. global config: `search.force_external: true`
3. provider config: `use_native_search: false`
4. default provider behavior

Example:

```yaml
search:
  force_external: true

providers:
  gemini:
    use_native_search: false
```

## Search in chat and agents

In chat mode:

- `Ctrl+S` toggles web search
- `/search` toggles web search

Agents can also enable search in their configuration:

```yaml
search: true
```

That exposes web search and page-reading tools to the agent.

## When to prefer each mode

Use **native search** when:

- you want the provider’s built-in grounding behavior
- you trust the provider’s integrated search product
- you want fewer moving pieces

Use **external search** when:

- you want one search stack across providers
- you need provider-independent behavior
- you want to debug the search pipeline more explicitly

## Related pages

- [Providers and models](/reference/providers-and-models/)
- [Configuration](/reference/configuration/)
- [Usage](/guides/usage/)
