# General ACP Support and Grok Migration Plan

Status: implemented
Prepared: 2026-07-14
Primary goal: obtain accurate Grok usage while introducing ACP as a reusable, security-conscious provider mechanism rather than a Grok-only protocol implementation.

Implementation note (2026-07-14): the reusable ACP v1 client and Grok adapter described here are now implemented. The dependency spike rejected `coder/acp-go-sdk` for this integration because its pinned release has a fixed 10 MiB frame limit, no transport close API, and non-lossless generic `_meta` handling. term-llm therefore implements the required client subset in `internal/acp` with a 64 MiB bounded frame reader and raw metadata preservation. Grok uses ACP by default for text/tool turns; image turns retain the legacy headless path because Grok 0.2.93 advertises `promptCapabilities.image: false`. Credentialed fresh, resumed, and MCP tool-loop probes against Grok 0.2.93 confirmed that final prompt metadata is aggregated for the ACP turn, `inputTokens` includes `cachedReadTokens`, and `reasoningTokens` is a detail within `outputTokens`. Grok's installed ACP documentation and live smoke tests also confirmed the `session/new` `_meta.systemPromptOverride` extension. Credentialed text, MCP tool, and ACP → legacy image → ACP continuity smoke tests passed against Grok 0.2.93. A follow-up probe against Grok 0.2.101 confirmed that `web_search`, `web_fetch`, and backend `x_search` work through the same restricted ACP profile. They are enabled only when `Request.Search` selects provider-native search; all local/action tools remain term-llm-owned through MCP.

## 1. Outcome

Add a general Agent Client Protocol (ACP) client layer to term-llm and migrate the existing `grok-bin` provider from Grok's lossy `--output-format streaming-json` wrapper to `grok agent stdio`.

The completed Grok path should be:

```text
term-llm engine
  -> Grok ACP adapter
    -> reusable ACP v1 client over stdio
      -> grok agent stdio
        -> xAI
```

The migration must preserve the current `grok-bin` user-facing provider name, model/effort behavior, term-llm-owned tools, durable conversation resume, isolation, cancellation, diagnostics, and cleanup. It should additionally emit accurate `EventUsage` from Grok's final ACP prompt metadata.

The ACP work must leave a clean seam for a second ACP agent. It must not prematurely claim that arbitrary ACP agents are interchangeable with ordinary stateless LLM APIs.

## 2. Scope

### In scope

- A reusable ACP v1 client-side transport/lifecycle package.
- Stdio subprocess transport with bidirectional JSON-RPC 2.0.
- Capability negotiation and strict capability gating.
- Authentication selection through an adapter/profile policy.
- Session create/load-or-resume/prompt/cancel lifecycle.
- Mapping ACP message/thought updates into `llm.Event` values.
- Preservation of unknown `_meta` data for provider-specific extensions.
- Grok-specific launch, auth, session configuration, usage extraction, and compatibility behavior.
- Existing term-llm tools supplied to Grok through the existing authenticated HTTP MCP bridge.
- Fake-agent protocol, lifecycle, cancellation, and regression tests.
- Documentation of unsupported ACP capabilities and safe degradation.

### Deferred, but the design must not preclude it

- User-configurable arbitrary ACP provider commands.
- A second production ACP profile (recommended proof target: Gemini CLI or Copilot CLI).
- Remote/custom ACP transports; stable v1 stdio is the only initial transport.
- Full generic ACP filesystem and terminal client implementations.
- Interactive ACP permission UX.
- Surfacing agent plans, slash commands, session lists, session deletion, or agent session titles in the TUI.
- Dynamically replacing term-llm's provider/model registry from ACP `configOptions`.

### Explicit non-goals for the Grok milestone

- Do not migrate `claude-bin` merely because it is another CLI provider; only migrate agents with a real ACP endpoint.
- Do not expose Grok's native filesystem, terminal, memory, planning, image, task, or subagent tools. Native read-only `web_search`, `web_fetch`, and `x_search` are the sole exception when provider-native search is enabled.
- Do not translate informational ACP `tool_call` updates into executable term-llm tool calls.
- Do not parse Grok debug logs. They contain credentials and are not a protocol surface.
- Do not synthesize billable token usage from ACP's standard `usage_update`; that update describes current context/cost state, not necessarily per-request token accounting.
- Do not make ACP protocol types depend on `internal/llm` types.

## 3. Protocol facts that drive the design

This plan targets ACP wire protocol version `1`. The schema release/version used to generate a library is distinct from the negotiated integer `protocolVersion`.

### 3.1 Transport and RPC

- ACP v1 uses bidirectional JSON-RPC 2.0.
- With stdio, each UTF-8 JSON message occupies exactly one newline-delimited line.
- Protocol stdout must contain only ACP messages; logs belong on stderr.
- Either endpoint can issue requests while another request is outstanding. In particular, term-llm must continue reading and service agent-to-client requests while waiting for `session/prompt`.
- Responses can arrive out of order and must be correlated by JSON-RPC ID.
- The initial implementation should emit individual JSON objects only, not JSON-RPC batches.
- ACP defines both feature-specific `session/cancel` and general `$/cancel_request`; cancellation is cooperative, and a prompt may still send final updates before its response.

Implementation consequence: a request/response helper wrapped around a synchronous stdout scanner is insufficient. The connection needs one continuous reader, serialized writes, a pending-request map, and independent inbound request/notification dispatch.

Frames may contain base64 images, embedded resources, diffs, terminal output, or large metadata. Do not use an unconfigured `bufio.Scanner` with its 64 KiB token limit. The transport must use a bounded line reader with a documented maximum (initially 64 MiB, configurable internally) and return a specific oversized-frame error.

### 3.2 Initialization and capabilities

Before any session operation, the client must call `initialize` with:

- `protocolVersion: 1`;
- only the client capabilities term-llm actually implements;
- `clientInfo` identifying term-llm and its version when available. Although currently optional, include it by default after verifying Grok accepts the field.

The agent returns the negotiated version, agent capabilities, authentication methods, and optional agent information. If the negotiated version is unsupported, close the process and report a clear incompatibility error.

Omitted or null capabilities mean unsupported. Optional methods must never be called merely because they are present in the schema.

For the first Grok milestone, advertise a deliberately narrow client capability set:

```json
{
  "fs": {
    "readTextFile": false,
    "writeTextFile": false
  },
  "terminal": false,
  "session": {
    "configOptions": {
      "boolean": {}
    }
  }
}
```

The exact boolean-config capability shape and Grok's behavior must be confirmed against the selected schema/SDK and the Phase 0 probe. Advertising it is safe only once term-llm can decode and set boolean session options correctly. If Grok's model/effort controls work through launch flags without it, omit the entire `session` capability for the first milestone. Do not advertise filesystem or terminal access until those methods are integrated with term-llm's permission and path-boundary systems.

### 3.3 Authentication

Authentication is agent-defined but negotiated:

1. Read `authMethods` from `initialize`.
2. Let the profile choose an advertised method.
3. Call `authenticate` with exactly that method ID.
4. Treat `auth_required` as a structured, potentially recoverable user-facing error.

The Grok profile should authenticate eagerly after initialization, matching xAI's documented sample. The generic layer may also support a lazy fallback: if a session operation returns `auth_required`, authenticate once through the profile and retry only that side-effect-free session setup operation. Never replay a prompt to recover authentication.

Grok's documented ACP IDs are:

- `xai.api_key` when API-key auth is intended and available;
- `cached_token` for an existing Grok login.

The current provider defaults to OAuth/cached login and deliberately removes `XAI_API_KEY` when `preferOAuth` is true. Preserve that policy. Grok-specific auth IDs and `_meta: {"headless": true}` belong in the Grok adapter, not the generic ACP package.

### 3.4 Sessions

Baseline agents support `session/new`, `session/prompt`, `session/cancel`, and `session/update`.

Two different restore operations exist:

- `session/load`: capability-gated and replays complete history as `session/update` notifications before returning.
- `session/resume`: separately capability-gated and restores without replay.

For term-llm, `session/resume` is preferable when advertised because term-llm already owns persisted scrollback. If only `session/load` is available, replay updates must be consumed without appending duplicate visible messages or usage. If neither is available, start a new agent session and reconstruct context from the term-llm transcript.

Session setup requires an absolute `cwd` and MCP server list. HTTP MCP is capability-gated; stdio MCP is baseline ACP functionality. Grok is expected to advertise HTTP MCP based on the observed initialization response, but Phase 0 must verify this under the isolated production-like environment. If HTTP MCP is absent, the implementation must either provide a safe stdio MCP adapter or retain the old Grok transport; it must not silently omit term-llm tools.

### 3.5 Prompt content

`session/prompt` accepts ordered MCP-compatible content blocks. Text and resource links are baseline; image, audio, and embedded resources are capability-gated.

Mapping rules for the first implementation:

- User/developer text: convert to ordered text blocks using the current Grok role policy until a general role policy is designed; ACP prompt itself represents a user turn and has no general system/developer message parameter.
- Images: send image blocks only when `promptCapabilities.image` is true; otherwise return the same clear unsupported-content error used by other providers rather than silently dropping user images.
- Files: prefer text/resource blocks only where semantics and capability support are known; preserve the current textual fallback for Grok initially.
- Assistant/tool history: do not resend when an ACP session was successfully resumed; on a new session, use the current Grok transcript reconstruction policy.
- System prompt: ACP v1 has no standard system-prompt field. Preserve Grok's behavior through a verified Grok-specific launch/config/session extension if available; otherwise include the current safe textual instruction strategy and record the semantic difference as a migration blocker.

### 3.6 Updates and completion

A prompt streams `session/update` notifications and finishes when the original `session/prompt` request receives a `PromptResponse` containing a stop reason.

Handle at least these standard update variants:

- `agent_message_chunk` -> `EventTextDelta` for text content.
- `agent_thought_chunk` -> `EventReasoningDelta` with raw reasoning kind for text content.
- `usage_update` -> retain as context telemetry; do not emit billable `EventUsage` without provider-specific evidence.
- `tool_call` / `tool_call_update` -> track for display/debug only in the Grok term-llm-owned-tool mode; never execute from these notifications.
- `plan` -> optional phase/debug event later; safe to ignore in the first milestone.
- `available_commands_update`, `current_mode_update`, `config_option_update`, `session_info_update` -> update internal session state where needed and otherwise safely ignore.
- Unknown standard or extension notifications -> ignore unless malformed in a way that prevents routing; preserve debug visibility without logging secrets.

Map final stop reasons deliberately:

- `end_turn`: successful completion.
- `max_tokens`: preserve streamed output and emit a warning phase.
- `max_turn_requests`: preserve streamed output and emit the existing Grok safety-budget warning.
- `refusal`: preserve output and complete normally unless current engine semantics require a user-facing warning.
- `cancelled`: return context cancellation without committing provider state.

Do not emit `EventDone` before the prompt response is received and all notifications already read before that response have been dispatched. The ACP guarantee permits updates after cancellation but before the prompt response.

### 3.7 Tools and permissions

ACP tool-call updates describe tools that the agent executes. They are not requests for term-llm to execute a model tool. Treating them as `EventToolCall` would cause duplicate effects.

For the Grok milestone, term-llm remains the sole owner of executable local/action tools; provider-hosted read-only search is the explicit exception:

1. Keep Grok native tools explicitly disabled through verified CLI/config controls, except `web_search`, `web_fetch`, and `x_search` while provider-native search is enabled.
2. Advertise ACP client filesystem and terminal capabilities as false.
3. Pass term-llm's authenticated MCP server in session setup.
4. Keep `cliToolBridgeState` as the authoritative path from an MCP invocation to `EventToolCall` and the engine executor.
5. Display correlated backend search/fetch `tool_call` status updates as informational execution events when native search is enabled. Use `rawOutput.name` and its serialized `input` when Grok supplies them, but never execute from these updates.
6. Start with automatic rejection/cancellation of unexpected `session/request_permission` requests. Receiving one in this mode should also produce a debug warning because it suggests the native-tool isolation is incomplete.

A later agent-owned mode may expose ACP permission, filesystem, and terminal capabilities, but it needs a separate security design. Permission options (`allow_once`, `allow_always`, `reject_once`, `reject_always`) do not map directly onto the current registry execution callback.

### 3.8 Extensibility and usage

ACP requires custom data to live under `_meta` and custom methods to begin with `_`. All protocol structs used at extension points must retain raw `_meta`, even when typed fields are also decoded.

Grok's final ACP prompt response currently includes provider metadata resembling:

```json
{
  "stopReason": "end_turn",
  "_meta": {
    "sessionId": "...",
    "requestId": "...",
    "totalTokens": 7372,
    "modelId": "grok-4.5",
    "inputTokens": 7333,
    "outputTokens": 38,
    "cachedReadTokens": 7296,
    "reasoningTokens": 30
  }
}
```

This is a Grok extension, not a portable ACP usage contract. The generic layer should return raw prompt metadata to the adapter. The Grok adapter should validate non-negative integers and initially normalize as:

```go
Usage{
    InputTokens:            max(0, inputTokens-cachedReadTokens),
    CachedInputTokens:      cachedReadTokens,
    OutputTokens:           outputTokens,
    ProviderRawInputTokens: inputTokens,
    ProviderTotalTokens:    totalTokens,
    ReasoningTokens:        reasoningTokens,
}
```

Before shipping this mapping, probe multiple new and resumed Grok sessions, reasoning efforts, and MCP tool turns to verify:

- whether `inputTokens` includes `cachedReadTokens`;
- whether output includes reasoning;
- whether counts are per model request, per ACP prompt turn, or current-session cumulative;
- whether multi-model-request tool loops are aggregated;
- why observed `totalTokens` may not exactly equal input plus output;
- whether missing/partial metadata occurs on cancellation, max-turn, refusal, or errors.

Emit no `EventUsage` when metadata is absent or invalid. Never replace a known count with a zero-valued usage event.

## 4. Proposed architecture

### 4.1 Package boundaries

```text
internal/acp/
    types.go          minimal stable ACP v1 types or SDK-facing aliases
    transport.go      transport interface and stdio NDJSON implementation
    connection.go     bidirectional JSON-RPC request correlation/dispatch
    client.go         initialize/auth/session/prompt operations
    errors.go         typed RPC, protocol, transport, and process errors
    testagent/        fake ACP agent helper used by integration tests

internal/llm/
    acp_provider.go   ACP-to-llm adapter/runtime shared by profiles
    acp_events.go     session update -> llm event mapping
    acp_prompt.go     reusable llm parts -> ACP content conversion
    grok_acp.go       Grok profile: command/auth/config/usage/quirks
    grok_bin.go       public provider and preserved Grok state/isolation helpers
```

`internal/acp` must not import `internal/llm`. It should be usable by future command/UI surfaces and should expose protocol concepts rather than term-llm events.

`internal/llm/acp_provider.go` owns the semantic adaptation to `Provider`, including stream lifecycle, transcript boundaries, tool bridge, and event emission.

`grok_bin.go` may remain the public provider file to avoid a disruptive rename. During migration, move only code whose ownership is clear; do not refactor unrelated CLI providers.

### 4.2 Go SDK decision

There is no official Go SDK, but the ACP site's community list names several Go implementations. The leading candidate for a spike is `github.com/coder/acp-go-sdk` because it provides typed bidirectional client connections, stdio support, extension metadata/method handling, examples, and an Apache-2.0 license. It is still a pre-1.0 community dependency.

Default recommendation: use a pinned community SDK behind a small `internal/acp` wrapper if it passes the gates below; do not expose its types throughout `internal/llm`.

Acceptance gates for the dependency spike:

- Implements the current stable protocol v1 method/type surface needed here.
- Preserves unknown `_meta` on `PromptResponse` and relevant nested values.
- Supports agent-to-client requests while a prompt call is outstanding.
- Has correct cancellation, close, EOF, and out-of-order response behavior.
- Does not use an unsafe 64 KiB scanner limit.
- Allows bounded frame sizes or can be safely wrapped.
- Does not log protocol payloads or credentials by default.
- Permits structured method-not-found responses for unimplemented client methods.
- Adds an acceptable dependency/maintenance footprint under the repository's Go version.
- Passes a real `grok agent stdio` smoke probe and fake-agent race tests.

If no SDK passes, implement only the required ACP client subset in `internal/acp`, using official schema fixtures and wire-level tests. Do not generate or hand-copy the entire schema merely to reach Grok.

### 4.3 Core interfaces

Keep the generic ACP layer policy-free:

```go
type ClientHandler interface {
    SessionUpdate(context.Context, SessionNotification) error
    RequestPermission(context.Context, RequestPermissionRequest) (RequestPermissionResponse, error)
    ReadTextFile(context.Context, ReadTextFileRequest) (ReadTextFileResponse, error)
    WriteTextFile(context.Context, WriteTextFileRequest) error
    CreateTerminal(context.Context, CreateTerminalRequest) (CreateTerminalResponse, error)
    // Remaining terminal methods...
}

type Client interface {
    Initialize(context.Context, InitializeRequest) (InitializeResponse, error)
    Authenticate(context.Context, AuthenticateRequest) error
    NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error)
    LoadSession(context.Context, LoadSessionRequest) (LoadSessionResponse, error)
    ResumeSession(context.Context, ResumeSessionRequest) (ResumeSessionResponse, error)
    CloseSession(context.Context, CloseSessionRequest) error
    Prompt(context.Context, PromptRequest) (PromptResponse, error)
    CancelSession(context.Context, SessionID) error
    Close() error
}
```

The exact shape should follow the selected SDK where useful, but the wrapper must retain context cancellation and raw extension metadata.

The LLM adapter/profile seam should isolate agent quirks:

```go
type acpProfile interface {
    BuildCommand(context.Context, Request, acpLaunchState) (*exec.Cmd, error)
    ClientCapabilities(Request) acp.ClientCapabilities
    SelectAuth(acp.InitializeResponse) (acp.AuthenticateRequest, bool, error)
    ConfigureSession(Request, acp.InitializeResponse) (acpSessionConfig, error)
    ConfigureOptions(context.Context, acp.Client, acpSessionState, Request) error
    PromptContent(Request, acp.AgentCapabilities, acpSessionState) ([]acp.ContentBlock, error)
    Usage(acp.PromptResponse) (*Usage, error)
    ClassifyError(error) error
}
```

Avoid a large public abstraction before Grok and a second profile validate it. Unexported interfaces are easier to revise.

### 4.4 Connection and process ownership

Use one ACP subprocess/connection per `GrokBinProvider` conversation, not one subprocess per prompt. This matches ACP's stateful design and avoids repeated initialize/auth overhead. The first implementation will keep that process until `CleanupMCP`, provider shutdown, transport failure, or an explicit idle timeout added after measurement. This is a deliberate resource tradeoff: every live conversation may hold an idle Grok process. `CleanupTurn` continues to remove only turn-scoped temporary files and must not close a healthy conversation connection.

Before reusing a connection, check its process/transport done signal. There is no standard ACP ping; a process can still die between the check and the write, so prompt setup must also classify write/EOF failure as a dead connection. Restart and restore automatically only before `session/prompt` has been accepted; after prompt submission, follow the ambiguous-side-effect rule and do not replay automatically.

Lifecycle:

```text
provider created
  -> first non-ephemeral Stream
     -> ensure isolated GROK_HOME/MCP
     -> start grok agent stdio
     -> initialize
     -> authenticate
     -> resume/load existing session or session/new
     -> apply model/effort config
     -> session/prompt
  -> later Stream
     -> reuse live connection/session
     -> apply changed model/effort if supported
     -> session/prompt
  -> transport failure
     -> discard connection
     -> restart agent
     -> resume/load persisted session if safe
  -> CleanupMCP/provider shutdown
     -> cancel active prompt
     -> close session if supported
     -> close stdin, terminate process group after grace period
     -> stop MCP
     -> retain durable GROK_HOME
```

Ephemeral requests must use a separate temporary ACP process/session and must not mutate the conversation's connection, session ID, `messagesSent`, or exported state. The current single-active-stream guard should remain initially; allowing an ephemeral request concurrently would require fully separate bridge and process ownership.

### 4.5 Provider state

Preserve the existing exported JSON shape if possible:

```json
{
  "grok_home": "...",
  "session_id": "...",
  "messages_sent": 12
}
```

Do not persist process-local connection IDs, request IDs, auth tokens, or capabilities.

On import:

- retain all existing path/symlink validation;
- if the durable home disappeared, clear session/message boundary and reconstruct from term-llm history;
- after process start, prefer `session/resume`, then `session/load`, then a new session with transcript reconstruction;
- commit `sessionID` and `messagesSent` only after a successful prompt response whose state is safe to resume;
- do not commit on cancellation, malformed protocol, auth failure, or an ambiguous transport disconnect.

When using `session/load`, suppress replay from user-visible output and usage accounting. Optionally compare replay against persisted term-llm history in debug/test code, but do not make exact textual equivalence a prerequisite because agents may normalize content.

## 5. Grok-specific migration requirements

### 5.1 Preserve unchanged behavior

The migration must preserve these existing properties from `internal/llm/grok_bin.go` and tests:

- Public provider key/credential: `grok-bin`.
- Open-ended model names and `parseGrokEffort` behavior.
- Request `ReasoningEffort` precedence over model suffix/default.
- `Capabilities`: tool calls, managed context, inline tool loop, no tool choice.
- OAuth preference and `SetPreferOAuth`.
- `GROK_AUTH_PATH` default and configured environment overrides.
- Forced isolated `GROK_HOME` and disabled updater.
- Removal of `XAI_API_KEY` when OAuth is preferred.
- Private durable home/config/cwd permissions and stale-home GC.
- Imported-home validation against traversal and symlink escapes.
- Disabled compatibility integrations in generated Grok config.
- Authenticated localhost HTTP MCP server and bearer token.
- Term-llm-owned local/action-tool deny policy with a provider-native read-only search exception.
- Text/image/tool-result prompt conversion and deferred interjections.
- Resume `messagesSent` boundary and ephemeral non-mutation.
- One active stream per provider.
- Process-group cancellation and bounded stderr diagnostics.
- Prompt/system-prompt/credential redaction.
- Max-turn warning behavior and preservation of partial output/tool effects.
- `CleanupTurn` temporary-file semantics and `CleanupMCP` retaining durable home.

### 5.2 Launch and capability probe before migration

Before changing the provider, write an opt-in developer probe (not a normal test) or manually record fixtures from the installed Grok version for:

1. Exact argument ordering accepted by `grok agent stdio` for model, effort, `--no-auto-update`, profile, system prompt, max turns, and native-tool restrictions. Use the flag as well as `GROK_DISABLE_AUTOUPDATER=1`: any updater banner on stdout would corrupt ACP framing.
2. `initialize` response capabilities and auth methods under isolated `GROK_HOME` plus external `GROK_AUTH_PATH`; verify Grok accepts `clientInfo` and the exact `clientCapabilities.session.configOptions.boolean` shape.
3. `session/new` response config options and mode/model identifiers, including whether advertising boolean config support is necessary before Grok exposes or accepts `session/set_config_option`.
4. Whether HTTP MCP configuration works solely through `session/new`.
5. Whether native Grok tools can be completely disabled in ACP mode.
6. Whether a neutral ACP `cwd` remains effective while MCP tools execute in term-llm's actual working directory.
7. New, resume, and load behavior across process restart.
8. Full notification ordering around text, reasoning, MCP calls, cancellation, and completion.
9. Final `_meta` usage across the cases listed in section 3.8.
10. Error shapes for missing login, unsupported model/effort, dead session, and max-turn limits.

Sanitize fixtures before committing: replace session/request IDs, paths, hostnames, and all auth values. Never enable Grok debug logging in automated probes because current debug logs expose credentials.

Native-tool isolation is a release blocker. If ACP mode cannot disable Grok's own side-effecting tools, do not replace the current provider. Resolve through a Grok agent profile/config or upstream capability first.

### 5.3 Model and effort configuration

Do not assume launch flags are the only or best control. After `session/new`/restore, inspect `configOptions` categories:

- `model` for model selection;
- `thought_level` for reasoning effort;
- possible Grok-specific IDs in `_meta`.

Prefer standard `session/set_config_option` when the exact requested values are advertised. Use launch flags only when required to establish initial options. Validate requested values rather than silently accepting an agent fallback, and update internal state if `config_option_update` reports an agent-initiated fallback.

If Grok exposes both legacy `modes` and config options, use config options and ignore modes as recommended by ACP.

### 5.4 System prompt and safety budget

The current provider passes `--system-prompt-override` and `--max-turns 30`. ACP v1 does not standardize either setting.

Both require explicit probe results before cutover:

- If Grok accepts equivalent launch flags before `agent stdio`, preserve them there.
- If Grok advertises config options/extensions, use those.
- If neither exists, do not silently drop the system override or 30-turn safety budget. Keep the old transport until an upstream mechanism exists, or implement a clearly tested Grok-specific prompt fallback only if it preserves the security model.

### 5.5 Tool event ordering

ACP updates and HTTP MCP requests arrive on independent channels. Preserve the existing invariant that text introducing a tool appears before the authoritative `EventToolCall` where possible.

The adapter should multiplex:

- ACP notification stream;
- MCP `cliToolRequest` channel;
- prompt completion;
- context cancellation;
- process/connection failure.

Reuse `cliToolBridgeState` and `cliTurnBridge` with an explicit drain/barrier strategy. Do not blindly retain the current 25 ms grace if ACP provides stronger ordering identifiers; first record real Grok ordering. Add deterministic fake-agent tests rather than timing-only tests.

## 6. Error, cancellation, and security design

### 6.1 Error taxonomy

Introduce errors that retain enough structure for adapter classification:

- `RPCError`: code, message, raw data, method, request ID.
- `ProtocolError`: malformed envelope, invalid version, duplicate/unknown response ID, invalid required fields.
- `TransportError`: EOF, oversized frame, write failure.
- `ProcessError`: command, exit code, bounded/redacted stderr tail.
- `CapabilityError`: attempted unsupported feature.
- `AuthenticationError`: no usable auth method or auth-required response.

Unknown response IDs should be debug-visible and ignored if they can result from locally timed-out/cancelled requests; duplicate IDs and malformed envelopes should not corrupt other pending requests.

### 6.2 Cancellation sequence

When a term-llm prompt context is cancelled:

1. Mark the turn cancelling and stop accepting new MCP effects.
2. Send `session/cancel` once if a session/prompt is active.
3. Reply `cancelled` to all pending permission requests.
4. Continue reading and dispatching allowed final updates until the prompt response or a short grace deadline.
5. If no response arrives, send `$/cancel_request` for the prompt if supported by the connection implementation.
6. Close stdin and terminate the ACP process group after the process grace deadline.
7. Unblock pending MCP acknowledgements/results and all JSON-RPC callers.
8. Do not advance provider resume state.

Use bounded deadlines for handshake operations (`initialize`, eager `authenticate`, session create/load/resume/close, and config changes), with method-specific errors and process cleanup. Do not impose a fixed wall-clock or inactivity timeout on `session/prompt` initially: agent turns and term-llm tools may legitimately run for a long time, so user/context cancellation is the escape hatch, matching current CLI-provider behavior. Revisit an inactivity watchdog only with telemetry and a definition that excludes active tool work; regardless, the cancellation/process grace deadlines remain bounded.

A transport failure while a tool may have executed is ambiguous. Preserve already emitted tool effects/output, mark the connection unusable, and do not automatically replay the same prompt against a restarted agent.

### 6.3 Security boundaries

- Keep ACP stdout protocol-only; never echo raw frames to standard logs by default.
- Bound line/frame size, stderr tail size, pending request count, and terminal output if terminal support is later added.
- Serialize writes to prevent interleaved JSON.
- Use process groups and existing `procutil.PrepareCommand` cancellation behavior.
- Do not persist or log auth tokens, MCP bearer tokens, prompt text, raw image data, or secret-bearing `_meta`.
- Treat ACP requests containing `mcpServers` as secret-bearing because HTTP server headers contain the MCP bearer token; redact those params before any diagnostic or frame-level debug output.
- Redact environment and command diagnostics using the current Grok rules.
- Treat imported paths and provider state as untrusted.
- Do not advertise client capabilities whose permission/path semantics are not implemented.
- Reject agent filesystem/terminal requests with JSON-RPC method-not-found while those capabilities are false.
- Keep the MCP server localhost-only and authenticated.
- Treat an unexpected permission request or non-search native tool call as a potential isolation failure.

## 7. Implementation phases

Each phase should land independently with tests and preserve the existing Grok transport until the ACP path passes its acceptance gates.

### Phase 0: protocol and dependency spike

Deliverables:

- Record the ACP v1 schema/release baseline used for implementation.
- Evaluate `coder/acp-go-sdk` against section 4.2; compare another listed Go SDK only if a gate fails.
- Build a throwaway or test-only client against `grok agent stdio` without changing `grok-bin`.
- Capture sanitized initialize/session/prompt fixtures and usage semantics.
- Resolve native-tool disablement, system prompt, max-turn, model/effort, and MCP launch questions.
- Write an architecture decision note in this plan or code comments choosing pinned SDK versus local subset.

Exit gate: no unresolved security blocker and a known path to every current Grok invariant.

### Phase 1: reusable ACP transport/client

Tests first, then implementation:

- Bidirectional request/response correlation with out-of-order responses.
- Agent-to-client request while `session/prompt` is pending.
- Concurrent serialized writes.
- Notifications with no response.
- JSON-RPC errors and method-not-found.
- Clean EOF versus truncated/malformed frame.
- Large frame above 64 KiB and configured max-frame rejection.
- Context cancellation and late response handling.
- Process stderr isolation and bounded capture.
- Connection close unblocks every pending request and goroutine.
- `go test -race` for the ACP package.

Exit gate: fake client/agent conformance tests pass without Grok installed.

### Phase 2: ACP lifecycle and generic LLM adapter

Implement and test:

- initialize/version negotiation/capability gating;
- profile-selected authentication;
- new/load/resume selection;
- replay suppression for `session/load`;
- prompt content conversion with image capability checks;
- update-to-event mapping for text and thought;
- final stop-reason mapping;
- raw `_meta` preservation;
- cancellation sequence;
- unexpected permission/fs/terminal request handling;
- persistent versus ephemeral process/session ownership;
- no executable `EventToolCall` from ACP tool notifications.

Use a scripted fake ACP subprocess under `internal/acp/testagent` or testdata helper. Avoid tests that depend only on in-process mocks; process framing and shutdown are central behavior.

Exit gate: an internal test profile can complete, cancel, resume, and fail turns through the normal `Provider.Stream` contract.

### Phase 3: Grok ACP adapter behind a temporary switch

- Add Grok launch/profile logic while preserving `NewGrokBinProvider` and factory behavior.
- Retain old streaming-json implementation temporarily as fallback/test oracle. Set a removal criterion when the switch is introduced: delete it no later than the first release in which ACP is the default and all cutover gates pass, unless a documented minimum-Grok-version compatibility requirement justifies one additional release.
- Preserve isolated `GROK_HOME`, config, MCP, env, state, and cleanup.
- Configure model/effort/system/safety budget using verified mechanisms.
- Extract and normalize Grok usage metadata.
- Multiplex ACP and MCP events with deterministic ordering.
- Add an opt-in environment flag or unexported test constructor to select ACP during soak testing; do not create a permanent user-visible duplicate provider name.

Required regression coverage:

- Port every existing `internal/llm/grok_bin_test.go` invariant.
- Add fake-Grok ACP tests for initialize/auth/session/config/prompt.
- Add multi-turn restart/resume and load-replay suppression tests.
- Add tool call, delayed output, multiple tools, and cancellation-at-each-boundary tests.
- Add malformed/missing/partial usage tests and exact normalization tests.
- Add concurrent normal/ephemeral stream rejection tests.
- Add retry-wrapper state/tool/cleanup interface forwarding tests.
- Add an engine-level inline tool-loop test through the ACP adapter.

Exit gate: all hermetic tests pass and opt-in real Grok smoke tests show no behavioral regression.

### Phase 4: Grok cutover and cleanup

- Make ACP the implementation for `grok-bin`.
- Remove old streaming-json parser/command code only after a soak period or retained compatibility decision.
- Remove the stale comment claiming Grok supplies no usage.
- Keep a clear version error for Grok releases without usable ACP support; do not silently fall back in a way that can duplicate a partially executed turn.
- Update provider diagnostics to report negotiated ACP version and agent version without secrets.
- Verify session stats, context displays, persisted metrics, and cost calculation receive `EventUsage`.
- Run `gofmt -w .`, `go test ./...`, `go test -race ./internal/acp ./internal/llm`, and `go build`.

Exit gate: normal and resumed Grok turns report sensible token counts and all existing provider behavior is preserved.

### Phase 5: prove generality with a second ACP agent

Before exposing arbitrary ACP config, add one second profile using the same generic layer. Gemini CLI is a strong candidate because a community Go SDK already exercises it; Copilot is another candidate with official ACP documentation.

The second profile should require no changes to `internal/acp` beyond newly encountered standard protocol coverage. Provider quirks must remain in the profile. If the second integration requires broad changes to Grok-specific assumptions in `acp_provider.go`, revise the seam before adding user-defined agents.

Only then consider:

```yaml
providers:
  my-agent:
    type: acp
    command: ["some-agent", "acp"]
    env: {}
    tool_ownership: term-llm
```

Arbitrary commands introduce additional trust, capability, auth, state, and permission UX concerns and should be a separate feature.

## 8. Test matrix

### Protocol unit tests

- String and integer JSON-RPC IDs accepted; client emits one consistent form. Accept integral numeric echoes leniently (for example `1` versus `1.0`) without accepting fractional request IDs as locally generated IDs.
- Out-of-order response correlation.
- Duplicate, unknown, null, and malformed IDs.
- Requests, responses, notifications, and errors.
- Unknown extension notifications ignored.
- Unknown extension requests return method-not-found.
- Large text/image frame and oversized-frame rejection.
- Writer concurrency produces valid independent lines.
- Reader/protocol failure rejects all pending calls exactly once.

### Lifecycle tests

- Initialization must precede sessions.
- Unsupported negotiated protocol version closes connection.
- Every optional method is capability-gated.
- Auth method selection and no-compatible-method error.
- New session, resume without replay, load with replay suppression.
- Dead persisted session falls back only when safe.
- Prompt updates precede completion.
- Cancellation allows final updates but does not commit state.
- Ephemeral session cannot mutate persistent state.
- Process death during prompt does not automatically replay.

### Event mapping tests

- Text and thought chunks, including multiple message IDs.
- Non-text content is handled without panics or accidental data loss.
- Standard `usage_update` does not become billable usage.
- ACP tool updates never execute tools.
- MCP bridge remains the sole executable tool event source.
- Max-token/max-request/refusal/cancelled stop reasons.
- Unknown updates are ignored and optionally debug logged.

### Grok tests

Retain or adapt all existing tests for:

- model/effort parsing and precedence;
- capabilities;
- environment/auth isolation;
- native-tool disabling;
- private config/home layout;
- prompt/image conversion;
- resume boundaries and deferred interjections;
- command errors and redaction;
- max-turn partial success;
- state path validation and missing-home recovery;
- stale-home GC and cleanup;
- ephemeral state isolation.

Add:

- documented Grok ACP auth method choice;
- config option selection for model/effort;
- HTTP MCP setup capability check;
- usage extraction for cached/reasoning/total tokens;
- no usage event on absent metadata;
- multi-request tool-loop usage semantics;
- restart and `session/resume`/`session/load` behavior;
- unexpected permission/native tool isolation failure;
- clean recovery after cancellation without stale bridge state.

### Manual/opt-in smoke matrix

Against a pinned minimum and current Grok CLI:

- cached-token OAuth and API key modes;
- new and resumed text turns;
- low/medium/high reasoning;
- image prompt;
- one and multiple term-llm tools;
- tool failure;
- cancellation before output, during output, and during a tool;
- invalid model and expired auth;
- process kill/restart;
- token values compared with sanitized ACP response metadata.

Tests requiring live credentials must never run in `go test ./...`.

## 9. Observability and rollout

Add debug-safe fields, not raw frames:

- agent executable and redacted args;
- ACP negotiated protocol version;
- agent name/version;
- selected auth method ID (never credential material);
- session operation chosen (`new`, `resume`, `load`);
- session ID only under existing debug/privacy policy;
- advertised capability summary;
- prompt stop reason;
- whether usage metadata was present/valid;
- bounded/redacted stderr tail on failure.

During temporary dual-path testing, compare:

- final text/reasoning behavior;
- tool event ordering and effects;
- session continuation after restart;
- cancellation latency and leaked processes;
- token counts and context status;
- error quality for auth/model failures.

Do not automatically retry a prompt on a new transport after any output or possible tool effect. Existing retry behavior must respect ACP's ambiguous side-effect boundary.

## 10. Known limitations after the Grok milestone

- ACP agents manage their own context and often their own multi-model-request tool loop; they are not drop-in stateless model APIs.
- ACP v1 has no portable system/developer prompt, temperature, top-p, forced tool choice, or exact per-turn token accounting contract.
- Standard `usage_update` reports context usage (`used`/`size`) and optional cumulative cost, not the normalized token buckets term-llm stores.
- Tool-call notifications are observational; tool execution remains agent-owned unless an MCP bridge is explicitly supplied.
- Client filesystem methods include unsaved-editor semantics that a terminal app does not naturally have.
- ACP terminal methods require lifecycle, output retention, process-group, permission, and path policies beyond simply calling `exec.Command`.
- Session replay may not exactly match term-llm's persisted transcript, and message IDs are opaque.
- Config option IDs and provider `_meta` are agent-specific and can change independently of ACP v1.
- Community Go SDKs are pre-1.0 and can lag schema changes; the wrapper and protocol fixtures are the compatibility boundary.
- Stdio process startup and auth remain agent-specific despite the shared wire protocol.

## 11. Definition of done for the requested goal

The work is complete when:

1. `grok-bin` communicates through `grok agent stdio` using a reusable ACP v1 client layer.
2. Grok emits normalized input, cached input, output, reasoning, and provider total usage when metadata is present.
3. Missing usage remains missing rather than becoming misleading zero counts.
4. Existing Grok tools still execute only through term-llm's permission-checked MCP bridge.
5. Native Grok side-effecting tools remain disabled.
6. Existing Grok session resume, isolation, auth, model/effort, image, cancellation, diagnostics, state security, and cleanup behavior is preserved.
7. The generic ACP layer has hermetic bidirectional, cancellation, large-frame, and process-lifecycle tests.
8. No live credentials are required for the normal test suite.
9. `go test ./...`, targeted race tests, and `go build` pass.
10. The abstraction can plausibly host a second ACP profile without importing Grok policy into `internal/acp`.

## 12. Primary references

ACP official specification and schema:

- Overview: https://agentclientprotocol.com/protocol/v1/overview
- Initialization/capabilities: https://agentclientprotocol.com/protocol/v1/initialization
- Authentication: https://agentclientprotocol.com/protocol/v1/authentication
- Session setup/load/resume/close and MCP: https://agentclientprotocol.com/protocol/v1/session-setup
- Prompt lifecycle, stop reasons, cancellation, and usage updates: https://agentclientprotocol.com/protocol/v1/prompt-turn
- Content blocks: https://agentclientprotocol.com/protocol/v1/content
- Tool calls and permission requests: https://agentclientprotocol.com/protocol/v1/tool-calls
- Filesystem client methods: https://agentclientprotocol.com/protocol/v1/file-system
- Terminal client methods: https://agentclientprotocol.com/protocol/v1/terminals
- General cancellation: https://agentclientprotocol.com/protocol/v1/cancellation
- Session modes: https://agentclientprotocol.com/protocol/v1/session-modes
- Session config options: https://agentclientprotocol.com/protocol/v1/session-config-options
- Extensibility and `_meta`: https://agentclientprotocol.com/protocol/v1/extensibility
- Stdio transport: https://agentclientprotocol.com/protocol/v1/transports
- Stable method map used during research: https://raw.githubusercontent.com/agentclientprotocol/agent-client-protocol/schema-v1.19.0/schema/v1/meta.json
- Community Go libraries: https://agentclientprotocol.com/libraries/community

Grok:

- Official headless/ACP documentation and sample client: https://docs.x.ai/build/cli/headless-scripting#acp

Candidate Go SDK:

- `coder/acp-go-sdk`: https://github.com/coder/acp-go-sdk

Repository implementation anchors:

- Provider interfaces and `Usage`: `internal/llm/types.go`
- Engine optional interfaces/tool wiring: `internal/llm/engine.go`
- Current Grok provider: `internal/llm/grok_bin.go`
- Current Grok tests: `internal/llm/grok_bin_test.go`
- Shared CLI/MCP bridge: `internal/llm/cli_bin_shared.go`
- Process-group setup: `internal/procutil/`
- HTTP MCP server: `internal/mcphttp/http_server.go`
- Provider factory/config/model registry: `internal/llm/factory.go`, `internal/config/`, `internal/llm/models.go`
