---
title: "Installation"
weight: 2
description: "Install term-llm with the one-liner, `go install`, or a local source build."
kicker: "Install"
source_readme_heading: "Installation"
featured: true
next:
  label: Provider setup
  url: /getting-started/providers-and-setup/
---
### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/samsaffron/term-llm/main/install.sh | sh
```

Or with options:

```bash
curl -fsSL https://raw.githubusercontent.com/samsaffron/term-llm/main/install.sh | sh -s -- --version v0.1.0 --install-dir ~/bin
```

### Go install

```bash
go install github.com/samsaffron/term-llm@latest
```

### Build from source

```bash
git clone https://github.com/samsaffron/term-llm
cd term-llm
go build
```
