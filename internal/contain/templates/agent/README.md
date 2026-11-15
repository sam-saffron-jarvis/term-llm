# term-llm agent contain workspace: {{name}}

This workspace runs the managed term-llm agent image.

## Before first start

The generated `.env` holds Web UI settings and collected credentials, with mode
`0600`. Keep it private. Add skipped provider credentials there, or seed host OAuth:

```sh
term-llm contain exec {{name}} seed-chatgpt-auth
term-llm contain exec {{name}} seed-copilot-auth
```

Host seeding uses `credentials_dir` from config.yaml, overridden by nonempty
`TERM_LLM_CREDENTIALS_DIR`; otherwise it uses the host config directory. Missing
credentials never fall back. Container destinations and first-boot hydration
paths stay unchanged; the host setting is not forwarded.

Seed recipes resolve `[[host_credentials_dir]]` at exec time. Existing workspaces
are not rewritten: update recipe `from` paths to
`[[host_credentials_dir]]/chatgpt_oauth.json` or
`[[host_credentials_dir]]/copilot_oauth.json`. Seeding does not rewrite `.env`.
Trust workspace recipes: `copy_files` reads declared host files and commands run
inside the container.

## Commands

```sh
term-llm contain start {{name}}
term-llm contain shell {{name}}
term-llm contain exec {{name}} agent
term-llm contain stop {{name}}  # keeps state
```

`contain exec` selects a matching `x-term-llm.exec_recipes` entry; otherwise it
runs the command directly. Use `--` to bypass a colliding recipe name:

```sh
term-llm contain exec {{name}} -- term-llm --version
```

After changing image settings (`TERM_LLM_VERSION`, `AGENT_DISTRO`,
`AGENT_PLATFORM`, or `AGENT_BASE_IMAGE`), rebuild:

```sh
term-llm contain rebuild {{name}}
```

Arch defaults to `linux/amd64`; Fedora uses native architecture and is recommended
for Apple Silicon. `AGENT_BASE_IMAGE` must match the selected distro.

## Script cells

New agents include `spawn_script`, `wait_script`, and `cancel_script`, with eight
live cells per session. Stop cancels them even while idle. Existing agent
configuration is not overwritten.

## Widgets

Widgets are enabled at `/home/agent/.config/term-llm/widgets/`, served under
`/chat/widgets/` (or `WEB_BASE_PATH` plus `/widgets/`). The `widgets` skill covers
creation, restart, and testing.

## Bootstrap model

First boot copies bootstrap files into persistent `/home/agent` for `{{name}}`.
Later boots use the volume's `.config/term-llm/init.sh`. Runit stays root; shells,
Web UI, jobs, and bootstrap-jobs run as `agent`. Use passwordless `sudo` when
needed. Shells use zsh; `tl` aliases `term-llm`.

For custom first-boot settings, mount `/seed` with a `bootstrap.yaml`.
