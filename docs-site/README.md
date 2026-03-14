# term-llm docs site

The docs site is built with Hugo.

## Local development

```bash
hugo server --source docs-site
```

## Production build

```bash
hugo --source docs-site --destination /tmp/term-llm-docs
```

All documentation content should live in Markdown under `docs-site/content/`.
