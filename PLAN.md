# Approval Mode Defaults Plan

## Goal

Make approval behavior consistent across term-llm while giving `chat` and `ask` an autonomous default experience without making approval policy depend on agent identity.

A blank configuration resolves to:

| Surface | Built-in mode |
| --- | --- |
| `chat` | `auto` |
| `ask` | `auto` |
| `edit` | `prompt` |
| `exec` | `prompt` |
| `loop` | `prompt` |
| `serve` | `prompt` |
| `serve mcp` | `prompt` |

`auto` remains guardian-reviewed approval, not yolo. Existing deterministic file/directory and shell allowlists continue to apply. `yolo` remains an explicit CLI-only choice.

Users can set one global default, override it per surface, and override either for one invocation.

## Product Decisions

These decisions are part of the implementation, not open questions:

1. A blank config uses the built-in surface matrix above.
2. An explicitly configured `approval.default_mode` applies to every surface that has no explicit surface override.
3. `chat` and `ask` are auto by built-in default; all other owning surfaces are prompt by built-in default.
4. `yolo` is not valid in config and is never restored on cold resume.
5. The rollout uses the brave default: existing configs with no explicit global or surface value begin using auto for new chat/ask runs after upgrade.
6. Existing configs with explicit `approval.default_mode: prompt` remain prompt for every surface without an explicit override.
7. Legacy resumed chat sessions whose stored approval mode is empty remain prompt; resuming an old session must not silently escalate it to auto.
8. Child/subagent runs inherit their parent approval manager and never independently escalate from a surface or agent default.
9. Interactive guardian initialization failure falls back to prompt with one visible warning per runtime/session. Headless guardian initialization failure returns a startup error. Runtime guardian review failures remain fail-closed according to existing interactive/headless approval-manager behavior.
10. Persist the requested/resolved session policy (`auto`), not a temporary runtime fallback (`prompt`) caused by guardian unavailability, so a later resume can retry guardian setup.

## Configuration Model

Both global and per-surface values are optional. Absence must remain distinguishable from explicitly configured `prompt` or `auto`.

Supported configuration:

```yaml
approval:
  default_mode: prompt # optional: prompt|auto

chat:
  approval_mode: auto # optional: prompt|auto

ask:
  approval_mode: auto # optional: prompt|auto

edit:
  approval_mode: prompt # optional: prompt|auto

exec:
  approval_mode: prompt # optional: prompt|auto

loop:
  approval_mode: prompt # optional: prompt|auto

serve:
  approval_mode: prompt # optional: prompt|auto
  mcp:
    approval_mode: prompt # optional: prompt|auto
```

`serve.mcp.approval_mode` configures the `term-llm serve mcp` command; it is distinct from the existing top-level `mcp` client configuration.

An omitted or empty surface value inherits an explicitly configured `approval.default_mode`. If neither is configured, use the built-in surface default table.

Do not accept `yolo` in configuration. This preserves the existing rule that yolo cannot become a persistent or cold-resume default.

### Examples

Blank config:

```yaml
{}
```

resolves `chat` and `ask` to auto and all other owning surfaces to prompt.

Force all surfaces to prompt unless individually overridden:

```yaml
approval:
  default_mode: prompt
```

Make all surfaces auto except the MCP server:

```yaml
approval:
  default_mode: auto

serve:
  mcp:
    approval_mode: prompt
```

Make only chat auto:

```yaml
approval:
  default_mode: prompt

chat:
  approval_mode: auto
```

## Resolution Precedence

Use one shared resolver with this precedence, highest first:

1. Explicit CLI approval mode.
2. Persisted session approval mode, where the surface supports resume.
3. Explicit per-surface `approval_mode`.
4. Explicit global `approval.default_mode`.
5. Built-in surface default.

For chat, an explicit CLI choice overrides the resumed session value. A stored `prompt` or `auto` overrides configuration when no CLI choice was supplied.

A resumed session with an empty legacy approval value is a compatibility case and resolves to prompt rather than falling through to the new chat built-in auto default. Stored yolo also resolves to prompt unless yolo is explicitly requested for the current process.

## CLI Model

Add a common enum flag:

```text
--approval prompt|auto|yolo
```

Keep the existing flags as compatibility aliases:

```text
--auto  == --approval auto
--yolo  == --approval yolo
```

`--approval`, `--auto`, and `--yolo` are mutually exclusive whenever more than one is explicitly present, even if their values agree. Reject all of these:

```text
--approval prompt --auto
--approval auto --auto
--approval auto --yolo
--auto --yolo
```

The new flag enables a one-off downgrade from the chat/ask built-in auto default:

```bash
term-llm chat @developer --approval prompt
term-llm ask @developer --approval prompt "fix this"
```

Use Cobra's `Flags().Changed(...)` state; do not infer explicitness from a boolean or string value.

### Common/pre-command flag wiring

Update `cmd/flags.go` so `--approval` works wherever the existing pre-command `--auto` and `--yolo` flags work:

- rename or supersede `CommonYolo` with `CommonApproval`;
- add an `Approval *string` binding alongside the compatibility boolean bindings;
- register `approval` as `flagKindString`, `PreCommand: true` in `commonFlagMetas`;
- mark all three flags mutually exclusive;
- update pre-command normalization tests;
- remove superseded common-flag paths rather than leaving two independent mode systems.

## Internal Design

### Approval surface and resolved value

Add a surface type and central resolver in `cmd/approval_mode.go`:

```go
type approvalSurface string

const (
	approvalSurfaceChat     approvalSurface = "chat"
	approvalSurfaceAsk      approvalSurface = "ask"
	approvalSurfaceEdit     approvalSurface = "edit"
	approvalSurfaceExec     approvalSurface = "exec"
	approvalSurfaceLoop     approvalSurface = "loop"
	approvalSurfaceServe    approvalSurface = "serve"
	approvalSurfaceServeMCP approvalSurface = "serve_mcp"
)
```

Return mode and source so callers can report the decision, distinguish requested policy from runtime fallback, and produce useful diagnostics:

```go
type resolvedApprovalMode struct {
	Mode   tools.ApprovalMode
	Source approvalModeSource
}
```

Sources:

```text
cli
session
legacy_session
surface_config
global_config
builtin_default
```

The resolver input carries explicit CLI state rather than reading command-global variables directly. Keep it deterministic and table-testable.

### Preserve unset configuration

The current schema eagerly defaults `approval.default_mode` to `prompt`. Change `internal/config/schema.go` from `def("approval.default_mode", ...)` to an optional key so blank YAML remains distinguishable from explicit global prompt.

Add optional per-surface fields to the relevant config structs. Add the smallest `LoopConfig` and nested serve-MCP config needed if those structures do not already exist. Prefer empty string for unset if it survives the Viper/mapstructure pipeline; otherwise use pointers or explicit presence metadata. Do not use eager Viper defaults for these keys.

Update or replace existing contradictory config tests, including:

- the canonical-default coverage entry for `approval.default_mode` in `internal/config/config_test.go`;
- `TestApprovalDefaultModeDefaultIsPrompt`, which must be replaced with an unset-value/effective-matrix test;
- config reset/generated-key assertions that currently expect `approval.default_mode` to be materialized.

Generated/reset config should omit optional approval-mode keys rather than writing a global prompt that disables the built-in chat/ask matrix. Documentation and `config get/show` should make effective defaults discoverable.

Validate configured non-empty values centrally. Unknown values return a clear configuration error rather than silently falling back:

```text
invalid chat.approval_mode "automatic": expected prompt or auto
```

Config completion suggests only `prompt` and `auto`; never `yolo`.

### Guardian provider and failure behavior

Blank approval config does not mean blank provider resolution. Guardian setup uses the existing provider fallback chain:

1. `guardian.provider`, when configured;
2. the active surface provider/model;
3. `default_provider` and its active model.

Thus a working primary LLM configuration normally provides a guardian without separate guardian configuration. If the primary provider itself cannot initialize, command startup already fails before autonomous execution.

When effective mode is auto:

- **Interactive runtime:** attempt guardian setup once during runtime initialization. On failure, set actual runtime mode to prompt and emit one warning explaining that requested auto is unavailable. Do not repeat the warning per tool action.
- **Headless runtime:** if guardian cannot initialize, return a startup error before accepting work. Do not start in a degraded mode that merely denies every unmatched command.
- **Runtime review failure after successful initialization:** preserve current fail-closed behavior. Interactive flows may prompt only where the existing approval manager explicitly supports that fallback; headless flows deny.

The resolved value records the requested policy. The `ApprovalManager` records the actual runtime mode. Keep these separate.

### Apply the resolved mode consistently

Create shared setup/application helpers around `ApprovalManager`:

- `prompt`: set `ModePrompt`;
- `auto`: prepare guardian callbacks and set `ModeAuto` if initialization succeeds;
- `yolo`: set `ModeYolo` without guardian review.

Interactive chat must still prepare guardian callbacks while currently in prompt mode if runtime mode toggling can later enter auto. Model-switch refresh of guardian callbacks must continue to work.

Do not broaden auto into yolo. File read/write directory policy, project approvals, and deterministic shell allowlists remain unchanged. Auto continues to guardian-review only the operation classes supported today.

## Owning Surfaces and Inherited Runs

Resolve defaults exactly once at an owning surface boundary:

- `cmd/chat.go` → `chat`;
- `cmd/ask.go` → `ask`;
- `cmd/edit.go` → `edit`;
- `cmd/exec.go` → `exec`;
- `cmd/loop.go` → `loop`;
- `cmd/serve.go` → `serve` for web/API/Telegram and serve jobs;
- `cmd/serve_mcp.go` → `serve_mcp`.

Pass the resolved mode into `cmdRunner`/tool-manager setup; the shared runner does not invent a surface default.

These paths inherit an existing parent/runtime approval manager and do not resolve a new default:

- `spawn_agent` runs in `cmd/spawn_runner.go`;
- child-skill/isolated-skill runs;
- goal/runtime helpers attached to an existing serve/chat runtime;
- MCP sampling or other child requests tied to a parent runtime;
- serve jobs, which inherit the resolved `serve` mode unless a future job schema adds an explicit approval field.

`contain` is not an approval surface; a contained child process resolves the command it actually invokes.

Update all consumers of the current boolean globals/options, including:

- runtime chat mode toggles and replacement chat models;
- subagent runner and MCP sampling propagation;
- `ask_json.go` compatibility output;
- `cmdRunnerOptions` and request structs carrying `Auto`/`Yolo`;
- serve jobs wiring that currently forwards `serveAuto`/`serveYolo`.

Preserve existing public/wire fields where compatibility requires them, but derive them from one resolved mode. Do not leave independent booleans that can disagree with `--approval`.

Remove superseded chat-only resolver branches and duplicated per-command `if yolo / else if auto` setup once all owning surfaces use the shared path.

## Session Semantics

Chat persists the requested approval policy. Preserve these rules:

- A new chat session stores the resolved requested mode before any temporary guardian fallback.
- An explicit `--approval` choice wins when opening or resuming.
- Without a CLI choice, stored `auto` or `prompt` wins over current config defaults.
- A legacy session with empty stored mode resolves and persists as prompt when next safely updated.
- Stored yolo is not restored after cold resume; it resolves to prompt unless yolo is explicitly requested for this process.
- Runtime user toggles update both requested and actual mode and continue updating the persisted session.
- Guardian initialization fallback changes only actual runtime mode; it must not overwrite persisted requested auto with prompt. A later resume retries guardian setup.

Review ask persistence but do not invent resume precedence for surfaces that do not currently restore approval mode.

## User Visibility

The actual active approval mode remains visible in the chat UI/footer from the first frame. If requested auto falls back, the footer shows prompt and the runtime emits one warning.

Debug/verbose diagnostics for non-TUI commands include both requested mode/source and actual runtime mode. Avoid routine output on every successful invocation.

Update help and configuration documentation to explain:

- blank config gives chat/ask auto and other surfaces prompt;
- explicit `approval.default_mode` overrides the built-in matrix globally;
- per-surface values override the global value;
- `--approval` overrides config/session according to the precedence rules;
- auto is guardian-reviewed and is not yolo;
- `--approval prompt` is the one-off conservative override.

## Tests First

Add failing tests before implementation.

### Resolver table tests

Use table-driven tests in `cmd/approval_mode_test.go` covering:

1. Blank config: chat auto.
2. Blank config: ask auto.
3. Blank config: edit, exec, loop, serve, and serve MCP prompt.
4. Explicit global prompt overrides built-in chat/ask auto.
5. Explicit global auto makes an otherwise conservative surface auto.
6. Surface prompt overrides global auto.
7. Surface auto overrides global prompt.
8. Explicit CLI prompt overrides surface/global auto.
9. Explicit CLI auto overrides surface/global prompt.
10. Explicit CLI yolo wins and remains CLI-only.
11. Stored session prompt/auto overrides config when CLI is absent.
12. Explicit CLI overrides stored session mode.
13. Empty legacy session mode resolves to prompt, not built-in chat auto.
14. Stored yolo resolves to prompt.
15. Invalid global and surface values return actionable errors.
16. A child with a parent approval manager does not resolve/escalate from its own surface default.

### Flag tests

Cover:

- `--approval` accepts `prompt`, `auto`, and `yolo`;
- invalid values fail;
- legacy `--auto` and `--yolo` map correctly;
- every pair among `--approval`, `--auto`, and `--yolo` conflicts, including agreeing values;
- explicit `--approval prompt` is detectable;
- pre-command normalization accepts and preserves `--approval`;
- renamed/superseded common flag-set wiring remains complete for every command.

### Guardian/application tests

Cover:

- blank-config chat and ask initialize auto using the active provider fallback when guardian-specific provider is unset;
- interactive guardian initialization failure yields actual prompt, requested auto, and exactly one warning;
- headless guardian initialization failure returns a startup error before work begins;
- headless runtime guardian review errors deny and never execute unreviewed actions;
- prompt-mode interactive chat can later toggle to auto because callbacks were prepared;
- model switching refreshes guardian callbacks without changing requested mode unexpectedly.

### Command integration tests

Add focused tests proving:

- blank-config chat and ask use auto when guardian is available;
- blank-config edit/exec/loop/serve/serve-MCP paths remain prompt;
- explicit global/surface/CLI settings flow through shared runners;
- serve jobs inherit resolved serve mode;
- child/subagent/skill runs inherit parent prompt/auto/yolo and cannot escalate;
- resumed chat precedence and legacy-session compatibility work;
- temporary guardian fallback does not persist prompt over requested auto;
- runtime user toggles do persist their selected mode.

### Configuration tests

Cover:

- blank YAML loads global and surface modes as unset;
- effective resolution still produces the built-in matrix;
- explicit global prompt/auto survive YAML round trips;
- config reset/schema omits optional approval keys rather than materializing prompt;
- completion suggests only `prompt` and `auto`;
- `yolo` and unknown values in config are rejected;
- replace `TestApprovalDefaultModeDefaultIsPrompt` and update canonical-default coverage accordingly.

## Compatibility and Rollout

Adopt the brave default for omitted settings:

- existing configs that omit both global and surface values use chat/ask auto for new runs after upgrade;
- existing configs with explicit `approval.default_mode: prompt` remain prompt;
- explicit surface values remain authoritative;
- legacy resumed sessions with empty approval mode remain prompt and therefore do not escalate on resume;
- new sessions use the new matrix and persist their requested mode.

Do not add install-age inference or a config-version migration for this rollout. Call the behavior change out prominently in release notes and highlight `--approval prompt` plus persistent global/surface opt-outs.

## Verification

After implementation:

1. Run `gofmt -w .`.
2. Run focused approval/config/command tests during development.
3. Run `go test ./...`.
4. Run the repository-standard `go build`.
5. Review `git diff` for duplicated/superseded approval branches and accidental API changes.
6. Manually smoke-test:
   - blank-config chat and ask show/use auto;
   - blank-config conservative surfaces remain prompt;
   - `--approval prompt` forces prompt;
   - explicit global prompt forces prompt across unset surfaces;
   - surface overrides work;
   - legacy empty-mode sessions resume in prompt;
   - guardian fallback warns once and does not persist the fallback;
   - yolo is never restored or enabled from config.

## Non-Goals

- Do not make approval mode depend on agent identity.
- Do not grant new filesystem directories merely because auto mode is active.
- Do not change guardian policy or broaden which operations guardian can approve.
- Do not make yolo configurable or persistent.
- Do not redesign the approval UI beyond exposing requested/effective mode where useful and showing required warnings.
