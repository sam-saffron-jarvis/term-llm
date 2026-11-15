---
title: "HTTP API endpoints and audit"
weight: 5
description: "API-root configuration, proxy path prefixes, credential boundaries, and built-in HTTP integration coverage."
kicker: "Providers"
---

`base_url` is an **API root**, including the API version and any gateway path
prefix. It is not a replacement hostname and is not a full operation URL.
For example, `https://gateway.example/team/gemini/v1beta/` produces
`/team/gemini/v1beta/models/MODEL:generateContent`. A trailing slash is optional;
fixed operation paths are appended without discarding the prefix. Do not add
`/models` to a Gemini root.

```yaml
providers:
  gemini:
    base_url: https://gateway.example/team/gemini/v1beta
  venice:
    base_url: https://gateway.example/team/venice/api/v1
image:
  gemini:
    base_url: https://image-gateway.example/gemini/v1beta
audio:
  elevenlabs:
    base_url: https://gateway.example/elevenlabs
search:
  provider: brave
  brave:
    base_url: https://gateway.example/brave/res/v1
```

## Resolution and compatibility

- Inference and model listing use the selected `providers.<name>.base_url`.
  Custom named native providers use their own entry, not the built-in name.
- Image, audio, music and embedding feature overrides take precedence over the
  same-named **native inference** `providers.<name>.base_url`, then the original public
  default. Only registered native inference types share roots: Flux, ElevenLabs,
  Jina and Voyage use their feature knobs and defaults, not dummy chat entries.
  Provider entries of another type are not reused. Venice embeddings retain
  their existing `providers.venice` root and credentials rather than a separate
  `embed.venice` block.
  Ollama embeddings retain their pre-existing independent
  `embed.ollama.base_url` and local default; they do not inherit the inference
  root or `OLLAMA_HOST`. Video retains its
  existing Venice image settings: `image.venice.base_url` then
  `providers.venice.base_url`.
- API-key OpenAI images and embeddings use their feature override, then
  `providers.openai.base_url`, `OPENAI_BASE_URL`, and the public v1 root.
  Existing shared headers and organization/project environment fallbacks remain.
  An explicit feature root does not resolve an unused shared root.
- ChatGPT embedding uses **only** `embed.chatgpt.base_url`, defaulting to the
  public OpenAI v1 root. It never inherits the Codex inference root,
  `OPENAI_BASE_URL`, or Platform organization/project headers.
- Search uses `search.<provider>.base_url`, then its original default; credentials
  retain their existing environment fallbacks. Exa MCP keeps its existing
  **full endpoint** `search.exa_mcp.url`, including any MCP path.
- YAML endpoint values use the existing lazy `${ENV}`, `file://`, `$()`, `op://`
  and `srv://` resolution. Loading an unused feature does not resolve its root.
  Only an unset endpoint selects its default. An explicitly configured endpoint
  that resolves empty (for example, a missing `${ENV}` or empty `file://` value),
  or fails resolution/validation, returns an error rather than falling back to
  a shared or public host.
  Native gateway completion and Venice image completion do not execute deferred
  endpoint references; pre-existing generic/Anthropic/Ollama completion refresh
  policies remain unchanged.
- All HTTP API roots, including existing `providers.<name>.base_url` settings,
  must be HTTP(S), with a hostname and without embedded credentials, a query
  or fragment. **Compatibility change:** URLs such as
  `https://user:password@gateway.example/v1` are no longer accepted. Remove
  URL userinfo and configure authentication through a provider-supported
  credential/header mechanism, or use a gateway that does not require URL
  userinfo. Operation parameters are encoded separately.
  Existing full-operation `url`, realtime WebSocket, Cloudflare template and
  remote-resource contracts remain separate; they are not renamed or migrated.
- Grok subscription overrides require explicit `type: grok`. An implicit legacy
  `grok` block with an API key or base URL still receives the migration hint;
  `type: xai` remains the public xAI API.

Setting an API root explicitly authorizes sending that integration's existing
API credentials to that root. Configure only gateways you trust. There is no
blanket transport-level host rewrite.

## Integration audit matrix

This matrix records the built-in clients, not arbitrary URLs a model or external
program may access. **Changed** means the public endpoint was hardcoded or a
factory/discovery path previously ignored configuration. **Existing** means the
endpoint contract was already configurable (with prefix normalization fixes
where noted). Test entries identify local regression coverage, not live-provider
certification. Existing provider tests continue to cover protocol features.

### Inference, discovery and setup

| Integration | Configuration / API-root contract | Audit disposition and local evidence |
|---|---|---|
| Anthropic Messages and models | `providers.<name>.base_url` (host-style root, appends `/v1/messages` and `/v1/models`); `ANTHROPIC_BASE_URL` fallback | Existing native options/SDK support; Anthropic request/header tests retained. |
| OpenAI Responses, models and WebSocket Responses (inline file inputs) | `providers.<name>.base_url`; `OPENAI_BASE_URL` fallback | Existing shared OpenAI endpoint support and Responses HTTP/WebSocket tests retained. |
| Gemini native inference | `providers.<name>.base_url`, default Google `/v1beta`; appends `/models/...` | Changed factory wiring; `TestNativeFactoryBaseURLInference`. |
| OpenRouter, xAI, Venice, NearAI, SambaNova, Obscurify | `providers.<name>.base_url`, retaining each adapter's original versioned default | Changed native wrapper/factory wiring without replacing provider-specific behavior; native factory and model-lister tests. |
| OpenCode Zen | `providers.<name>.base_url`, default `https://opencode.ai/zen/v1` | Changed; overridden roots list their own `/models` without hidden public catalog enrichment. Native factory/listing tests; custom completion catalogs use endpoint/credential-scoped caches instead of public refreshes. |
| OpenCode Go | `providers.<name>.base_url`, default `https://opencode.ai/zen/go/v1` | Existing multi-protocol support; custom roots keep Gemini requests on the same gateway rather than the default inference host. Existing protocol tests retained. |
| ChatGPT subscription | `providers.<name>.base_url`, default ChatGPT `/backend-api/codex` | Changed Responses/model routing; identity refresh is separate. Custom catalogs do not share public catalog cache. |
| Grok subscription | explicit `type: grok`, `providers.<name>.base_url`, default Grok CLI proxy `/v1` | Changed inference/models routing; identity refresh remains separate. Legacy ambiguous config remains rejected. |
| GitHub Copilot inference | `providers.<name>.base_url` | Changed inference/models override; default still uses the API endpoint obtained from token exchange (including business accounts). Exchange itself is an identity operation. |
| AWS Bedrock runtime | `providers.<name>.base_url` | Changed runtime override with original regional/China defaults and SigV4/bearer authorization retained; Bedrock factory request regression. AWS credential discovery is separate. |
| Ollama | `providers.<name>.base_url`, default local port 11434 | Existing support; normalize trailing slash before `/api/...`. |
| OpenAI-compatible and vLLM | `providers.<name>.base_url`; legacy full chat `url` retained | Existing API roots; retain model/reasoning controls and provider features. |
| Model command / setup listing | same selected provider root | Factory/listing paths use native options instead of constructing public-only wrappers. Setup retains configured endpoints. |
| CLI inference adapters | `claude-bin`, `grok-bin`, `cursor-bin`, `vibe-bin`, `agy-bin` | Intentional boundary: the subprocess owns its HTTP transport. Its existing `env` configuration is not rewritten. Cursor's built-in usage client is listed below. |

### Images, audio, video, music and embeddings

| Integration | Configuration / operation paths | Audit disposition and local evidence |
|---|---|---|
| Gemini images | `image.gemini.base_url`; `/models/MODEL:generateContent` | Changed; image factory request regression. |
| OpenAI images / edits | `image.openai.base_url`; `/images/generations`, `/images/edits` | Shared endpoint already existed; added feature override and deferred-precedence regression. Existing generation/edit tests retained. |
| ChatGPT images / edits | `image.chatgpt.base_url`; `/images/generations`, `/images/edits` under Codex root | Changed; dedicated OAuth image implementation retained. |
| xAI images | `image.xai.base_url`; `/images/generations` | Changed; image factory request regression. |
| Venice images, edits, processing, explicit media catalogs | `image.venice.base_url`; `/image/generate`, `/image/edit`, `/image/multi-edit`, `/image/upscale`, `/image/background-remove`, `/models?type=image\|inpaint\|video` | Offline fixtures; only explicit typed listing discovers/writes root-scoped cache. All media/catalog redirects refused; bounded bodies. |
| Flux/BFL image generation / editing / polling | `image.flux.base_url`, default BFL `/v1`; model operation plus `/get_result` | Changed; `TestFluxFactoryRoutesPollAndLeavesDownloadUncredentialed` verifies returned task query routes to configured API root, and signed image downloads get no API key. Unset/default BFL polling preserves validated HTTPS BFL regional endpoints; any explicit root (even the public default value) pins polling to that selected root. Authenticated cross-origin redirects are rejected; signed download URLs remain exact and uncredentialed. |
| OpenRouter images | `image.openrouter.base_url`; `/chat/completions` | Changed; image factory request regression; provider feature code retained. |
| Named OpenAI-compatible images | selected `providers.<name>.base_url` | Existing validated root contract and local factory tests. Debug images are local, not HTTP. |
| Venice TTS | `audio.venice.base_url`; `/audio/speech` | Changed; `TestConfiguredProvidersRequests`. |
| Gemini TTS | `audio.gemini.base_url`; `/models/MODEL:generateContent` | Changed; configured-provider request test. |
| ElevenLabs TTS and voice-name lookup | `audio.elevenlabs.base_url`; `/v1/text-to-speech/...`, `/v2/voices` | Changed including voice metadata; configured-provider regression asserts both paths. |
| Venice music / sound effects | `music.venice.base_url`; audio generation/polling routes | Changed configured factory; music configured-provider and existing polling tests. |
| ElevenLabs music | `music.elevenlabs.base_url`; `/v1/music` | Changed configured factory; configured-provider request test. |
| Venice video quote / queue / retrieve / complete | `image.venice.base_url` (shared media settings) | Offline lifecycle fixtures; retrieve never deletes. Opt-in complete follows durable local save. No live-provider certification. |
| OpenAI embeddings | `embed.openai.base_url`; `/embeddings` | Shared endpoint existed; feature override and factory success/precedence tests added. |
| ChatGPT OAuth embeddings | `embed.chatgpt.base_url`; `/embeddings` | Changed dedicated override, isolated from Platform environment settings and Codex root; OAuth embedding tests. |
| Venice embeddings | `providers.venice.base_url`; `/embeddings` | Existing constructor supported root, factory normalization hardened; embedding factory success test. |
| Gemini embeddings | `embed.gemini.base_url`; `/models/MODEL:batchEmbedContents` | Changed; embedding factory success test. |
| Jina / Voyage embeddings | `embed.jina.base_url`, `embed.voyage.base_url`; `/embeddings` | Changed; embedding factory success tests. |
| Ollama embeddings | `embed.ollama.base_url`; `/api/embed` | Existing independent lazy endpoint and local default; normalize trailing slash. Factory success test; explicit inference root does not silently change embedding destination. |

### Transcription, classification and realtime

| Integration | Configuration | Audit disposition / boundary |
|---|---|---|
| OpenAI transcription | `providers.openai.base_url` and shared OpenAI endpoint/header resolution | Existing API-key endpoint support. |
| Venice / ElevenLabs transcription | `transcription.venice.base_url`, `transcription.elevenlabs.base_url` | Changed feature roots; existing multipart dialects and credentials preserved. ElevenLabs transcription root includes `/v1` (default `https://api.elevenlabs.io/v1`), unlike its host-style TTS/music roots. |
| Mistral Voxtral transcription | `providers.mistral.base_url` | Existing configurable transcription endpoint. |
| Local whisper.cpp HTTP | `providers.local_whisper.base_url` | Existing `/inference` contract. `whisper-cli` is an external executable, not an owned HTTP client. |
| Named compatible transcription | Explicit provider `base_url` or legacy `url` | Missing/deferred-empty custom endpoints fail closed rather than sending their key/audio to public OpenAI. Legacy transcription `url` remains a root-plus-operation contract with literal/deferred parity. A differently typed provider's key is only reused with its explicit legacy `url`, never with a native public fallback. |
| ChatGPT dictation | `transcription.chatgpt.base_url`, default ChatGPT `/backend-api` (separate from Codex and identity refresh) | Changed backend request routing; same multipart/auth retry behavior. |
| TypeSafe / OpenAI Decisions / Cloudflare classification | `classify.providers.<name>.base_url` | Existing validated endpoint and factory contracts. Cloudflare retains `{account_id}` template and model route semantics; not an inference-provider root. |
| OpenAI Realtime | `live.openai.base_url` | Existing call/sideband root; independent of Platform inference defaults. |
| ChatGPT Realtime | `live.chatgpt.call_base_url`, `live.chatgpt.sideband_base_url` | Existing distinct call/control-channel roots; not collapsed into the Codex root. |
| Gemini Live | `live.gemini.base_url` | Existing **full WebSocket endpoint**, not an HTTP API root. |

Live roots now resolve deferred values at session start, not constructor/readiness.
`TestLiveFactoryResolvesEndpointsAtStart` exercises OpenAI, ChatGPT and Gemini
with a file-backed endpoint and nested prefix; existing call/sideband tests
cover their protocol-specific routes.

### Search and fetching

All changed search roots are covered by `TestFactoryBaseURLRequests` with nested
and trailing prefixes, Unicode and reserved query characters. Failure tests
cover invalid roots and HTTP 429. Exa MCP retains existing tests.

| Integration | Root setting | Default root / appended operation |
|---|---|---|
| Exa REST | `search.exa.base_url` | `https://api.exa.ai` + `/search` |
| Perplexity | `search.perplexity.base_url` | `https://api.perplexity.ai` + `/search` |
| Parallel | `search.parallel.base_url` | `https://api.parallel.ai/v1` + `/search` |
| Tavily | `search.tavily.base_url` | `https://api.tavily.com` + `/search` |
| Brave | `search.brave.base_url` | `https://api.search.brave.com/res/v1` + `/web/search` |
| Google Custom Search | `search.google.base_url` | `https://www.googleapis.com/customsearch/v1`; query parameters appended directly |
| You.com keyed / keyless | `search.youcom.base_url` | keyed `https://ydc-index.io/v1` + `/search`; keyless `https://api.you.com/v1` + `/agents/search` |
| Keenable keyed / keyless | `search.keenable.base_url` | `https://api.keenable.ai/v1` + `/search` (keyed) or `/search/public` (keyless) |
| DuckDuckGo Lite | `search.duckduckgo.base_url` | `https://lite.duckduckgo.com` + `/lite/` |
| Exa MCP search / fetch | `search.exa_mcp.url` | Existing full endpoint, default `https://mcp.exa.ai/mcp`; custom endpoints do not automatically receive hosted Exa credentials. |
| Jina Reader | `search.jina.base_url` | Changed: `https://r.jina.ai` + `/TARGET_URL`; local Reader request test. Public-target DNS/redirect preflight remains a deliberate SSRF check, not a hidden Jina API request. |

The historical missing-key fallback to DuckDuckGo remains for unconfigured
search. An explicit selected API-root override must instead report its missing
credentials; it must not silently contact a different public provider. A
configured DuckDuckGo root also applies when that fallback is used.

### Ancillary HTTP clients

These clients also run without loading inference configuration. Their explicit
environment API roots are resolved at request time, accept literal HTTP(S) URLs,
and retain their original defaults. They do **not** expand YAML deferred syntax.
They are separate from inference roots because their credentials and protocols
may be different.

| Integration | Explicit endpoint knob | Audit disposition / local evidence |
|---|---|---|
| GitHub skill discovery / Contents API | `GITHUB_API_URL` | Changed; shared API-root convention, nested-root and branch-query regression. Repository URL parsing remains `github.com`-only; this is not general enterprise-host URL parsing. |
| Constructed raw GitHub skill content | `TERM_LLM_GITHUB_RAW_BASE_URL` | Changed; separate uncredentialed content root, including newly generated install provenance used by future updates; skills regressions assert gateway routing and no API token on raw fetch. Existing explicit stored resource URLs are not migrated or rewritten; invalid configured roots fail provenance generation rather than falling back publicly. |
| SkillsMP registry | `SKILLSMP_BASE_URL` | Changed; registry search root and query/auth regression. |
| Official MCP registry | `MCP_REGISTRY_BASE_URL` | Changed `/v0/servers` root; `TestRegistryConfiguredRoot` verifies nested prefix and escaped query. |
| Telegram notify / bot polling / SDK file downloads | `TELEGRAM_BASE_URL` | Changed API root; notify and bot/file local request regressions. Only the SDK's synthesized fixed Telegram file URL is mapped to this root; arbitrary file-getter URLs are left intact. |
| Copilot GitHub billing usage | `GITHUB_API_URL`, existing `WithBaseURL` | Existing independent REST credential/endpoint contract; never uses chat OAuth credentials. |
| OpenRouter account analytics / credits / key usage | `OPENROUTER_USAGE_BASE_URL` | Changed **separate account API root**, default OpenRouter `/api/v1`; usage request regression; redirects remain disabled. Management keys are not silently forwarded to inference gateways. |
| Grok / OpenCode Go live usage | `providers.grok.base_url` (explicit `type: grok`), `providers.opencode-go.base_url` | Changed app propagation to the matching `/billing?format=credits` and `/usage` routes; local app/provider usage regressions. |
| ChatGPT account usage | `CHATGPT_USAGE_BASE_URL` | Changed dedicated account root, default `https://chatgpt.com/backend-api` + `/wham/usage`; local OAuth usage regression; never inherits Codex or Platform roots. |
| Cursor built-in usage client | `CURSOR_USAGE_BASE_URL` | Changed, default `https://api2.cursor.sh`; external Cursor inference CLI remains out of scope. |
| models.dev pricing catalog | `MODELS_DEV_BASE_URL` | Changed; `/api.json`, local pricing request regression. |
| OpenCode public model catalog | `OPENCODE_CATALOG_BASE_URL` | Changed separate uncredentialed catalog root plus `/api.json`, shared by default Go and Zen paths; local catalog regression. Explicit inference gateway roots suppress this public fallback. |
| LiteLLM pricing catalog | `LITELLM_PRICING_BASE_URL` | Changed; raw repository root plus the existing catalog path, local pricing request regression. |
| Release discovery / upgrade archives and checksums | `TERM_LLM_RELEASE_BASE_URL` | Changed; defaults to GitHub web root. Release discovery prefix regression; existing verified-download tests retained. |
| Published readable UI source | `GITHUB_API_URL` for release catalog; `TERM_LLM_RELEASE_BASE_URL` for archive/checksums | Changed fixed catalog/download construction; archive integrity and cache behavior retained. |

## Intentional exceptions and security boundaries

- OAuth login, device authorization, refresh, revocation, and Copilot token
  exchange are **identity operations**. They retain their identity issuer's
  endpoints; inference `base_url` must not reroute refresh tokens, device codes
  or GitHub account tokens. A proxy API root is not an OAuth issuer override.
- AWS credential resolution retains standard AWS IMDS/ECS endpoint controls
  (`AWS_EC2_METADATA_SERVICE_ENDPOINT`, container credential URI variables) and
  their trust restrictions. Bedrock runtime overrides do not reroute metadata.
- Returned image/audio/video signed downloads, GitHub content download URLs,
  SkillsMP raw resource URLs, and HTTP redirects used by verified release
  downloads are resource locations, not API roots. They are not arbitrarily
  rewritten. Downloads do not gain API authentication headers. A gateway that
  requires downloads to stay behind it must return suitable resource URLs.
- MCP servers/OAuth discovery, hub/node/delegation/jobs clients, peer broker,
  widgets, browser push subscriptions, user-provided web pages and remote assets
  already take explicit URLs from configuration, manifests, flags or protocol
  responses. There is no meaningful single provider root to substitute. Their
  existing trust/URL validation rules remain in force.
- GitHub Gist import/export/share, release-related git operations and inference
  CLI adapters delegate to `gh`/`git`/external executables. Configure those tools'
  hosts/proxy behavior separately. Share-preview readiness probes use the
  generated resource URL, not an owned GitHub REST endpoint.
- This is an endpoint-routing audit, not a new egress firewall, OAuth-issuer
  configuration system, universal redirect policy, or provider protocol
  certification. Tests use local servers and mocks only; no live service calls
  or credential migrations are needed.
