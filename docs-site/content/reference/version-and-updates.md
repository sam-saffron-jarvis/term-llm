---
title: "Version and updates"
weight: 3
description: "Check the installed version, upgrade term-llm, and disable automatic update checks."
kicker: "Lifecycle"
source_readme_heading: "Version & Updates"
next:
  label: Usage tracking
  url: /reference/usage-tracking/
---
term-llm automatically checks for updates once per day and notifies you when a new version is available.

```bash
term-llm version       # Show version info
term-llm upgrade       # Upgrade to latest version
term-llm upgrade --version v0.2.0  # Install specific version
```

To disable update checks, set `TERM_LLM_SKIP_UPDATE_CHECK=1`.
