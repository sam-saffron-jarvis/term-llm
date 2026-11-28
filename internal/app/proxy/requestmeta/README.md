# Passive HTTP request settings

`requestmeta` observes the bytes the ordinary upstream transport consumes. It
never pre-reads, starts a goroutine, decompresses, tokenizes, or calls a provider.
Install it only for `usage: auto`, after debug capture/body limits/counting, using
the actual rewritten path and endpoint relative to the configured upstream root.
It leaves request trailers, `GetBody = nil`, custody and SigV4 buffering unchanged.

The dispatch owner calls `Finalize` before terminal persistence. Transport `Close`
freezes observation and releases abandoned bytes, but never parses: HTTP/1.1 still
has its buffered tail and trailers to flush after body close. A completely read
body retains at most 1 MiB until owner finalization; there is no parsing goroutine.
Finalization detaches that buffer under a short lock, then parses outside the
transport lock with panic isolation. Read/Close never publish or mutate a record.
Late reads remain byte-transparent but cannot change the frozen evidence. Complete
known-length bodies do not require a final EOF. A short body, read failure, or
abandoned chunked body cannot be observed; cancellation of a response after full
request consumption does not undo valid request settings. Observation says
nothing about whether custody ultimately dispatched the request.

Transient JSON is capped at 1 MiB and depth 64. Over-budget known lengths allocate
no payload buffer. Unknown members are validated/skipped without materializing
prompt text or tool schemas. Duplicate names, invalid JSON, literal dotted-key
aliases, wrong scalar types, and unsupported enum/range values never become
settings. Cloud Code uses the outer model and `request` settings, not an inner
model. Gemini declared-tool counts count function declarations and the evidenced
native Google Search declaration; unknown tool variants suppress the total.
Tool counts are bounded to 256, concurrency to 128, setting integers to int32,
models to 128 safe-identity bytes, and retained scalar JSON to 512 bytes each (770 for a fully escaped model).
The finite emitted schema fits within 4 KiB. Buffers are discarded at finalization
or immediately on overflow; no raw body is persisted or exported.

`Metadata.state` is independent of response usage:

- `observed`: complete valid JSON and allowlisted settings. Absent fields were not sent.
- `not_observed`: no complete body was consumed (also the interpretation of legacy rows).
- `unsupported`: non-inference/method, WebSocket, content type, or content encoding.
- `truncated`: body budget, short read, or partial observation.
- `invalid`: malformed JSON or an uninterpretable allowlisted setting, including
  explicit null. Independently valid scalars remain available, but null must not
  silently become “Not sent.” This describes metadata evidence, not whether the
  upstream accepts the request; forwarding is unchanged.

Zero and false remain values. Gemini thinking `-1` is `dynamic`; Ollama output
`-1`/`-2` are `unlimited`/`context`, not missing or negative token counts. Advanced
Responses mode/context/multi-agent/cache-policy controls require the evidenced
OpenAI API GPT-5.6 origin/model. Compatible thinking template flags and budgets
are gated to the vLLM profile. Unknown endpoints/extensions are not inferred from
provider labels. Structural validation is scoped to the wire family; unrelated
extensions are skipped. No settings become pricing inputs or served-model fallbacks.

Gemini matches exact endpoint-relative `/models/`, `/v1/models/` and
`/v1beta/models/` RPC paths, splitting the final RPC colon. Bedrock splits escaped
`/model/<id>/invoke` or `/invoke-with-response-stream` segments before unescaping
the model, preserving encoded ARN/profile slashes. Opaque or ARN-shaped model
identities require the exact `anthropic_version: bedrock-2023-05-31` body marker
before Messages settings are interpreted. All model identities retain the same
128-byte allowlist bound; Converse and non-Anthropic schemas remain unsupported.

Run tests/benchmarks through isolated wrappers:

```sh
make test PKG='./internal/app/proxy/requestmeta ./internal/app/proxy/usagetap ./internal/app/proxy' ARGS='-race -count=1'
scripts/gotest.sh ./internal/app/proxy/requestmeta -run '^$' -bench . -benchmem
```
