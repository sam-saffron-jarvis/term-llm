# Embedded Web UI frontends

The chat application and Hub UI are authored in Preact and strict TypeScript under `frontend/src`. The Hub source of truth is `frontend/src/hub/`; its dashboard, bearer login, passkey setup/login/recovery, security administration, behavior, and styling must not be reintroduced into Go templates. Hub HTTP transport is owned by `frontend/src/api/hub-client.ts`.

Vite emits deterministic, minified production files into `internal/serveui/static/dist`. That generated directory is ignored by Git and embedded in the Go binary, so source builds require the frontend build while release binaries remain self-contained.

## Prerequisites

Source builds use Node 24 or newer and npm. The Go build step also writes the deterministic private UI-authoring source archive, which is embedded in release binaries for on-demand extension builder inspection, not downloaded by ordinary browser sessions. The test setup supplies isolated Web Storage implementations for Node 25+, whose process-level `localStorage`/`sessionStorage` globals otherwise depend on runtime flags. CI installs dependencies from `package-lock.json` with `npm ci`.

From the repository root, the normal build command installs locked frontend dependencies when needed, generates all three applications, and builds `./term-llm`:

```sh
make build
```

## Commands

```sh
make frontend             # chat, standalone Hub, standalone proxy, then precompression
npm --prefix frontend run format:check
npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run test:e2e
```

`typecheck` runs the TypeScript 7 native compiler from the `typescript` package.
JavaScript and TypeScript linting uses Oxlint; formatting remains in Prettier and
CSS linting remains in Stylelint. No TypeScript 6 compatibility compiler is needed.

From the repository root, `scripts/browser_lifecycle_smoke.sh` builds and starts a temporary `term-llm serve web --no-auth` process and runs the Playwright chat suite. `scripts/hub_browser_lifecycle_smoke.sh` exercises the bearer-authenticated Hub dashboard and a real proxied chat node, while `scripts/hub_passkey_smoke.sh` exercises passkey setup, login, recovery, and security administration with Chromium virtual authenticators. Run `make frontend-deps` first; standalone smokes fail immediately rather than downloading dependencies implicitly. On macOS the specs use an installed Google Chrome (`/Applications` or `~/Applications`), so no `playwright install` is needed; elsewhere, the smoke scripts use a `chromium` on `PATH`. `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/path/to/browser` overrides both. CI installs the Playwright-pinned browser.

The chat smoke runs mocked specs with `TERM_LLM_SMOKE_WORKERS` workers (default 4), then `real-server.spec.ts` and `webmcp.spec.ts` in one worker because they drive real provider runs on the shared fixture server. Smokes that delete their results default `TERM_LLM_E2E_ARTIFACTS=off` so green runs skip per-test trace and video recording; set it to `on` when you need them. `TERM_LLM_SMOKE_BINARY=/path/to/binary` (a `go build -tags browserfixture` binary) makes the chat, Hub and extension smokes skip their own asset and Go builds. The `iphone` project only runs specs whose titles mention iPhone or pinch; `mobile` covers generic touch layouts. Test browsers keep notifications inside Chromium instead of the desktop notification service.

The chat smoke uses HTTP transport only.

For native macOS Safari clipboard, toggle hit-target, and composer keyboard checks,
see [the actual Safari smoke](e2e/safari/README.md). It uses a locally bundled
production-component fixture and a Python stdlib W3C client, not Linux WebKit.

`scripts/check_frontend_network_policy.sh` fails closed unless `git grep` can scan production sources (tracked and untracked) and enforces transport ownership for `fetch`, browser fetch aliases, XHR, EventSource, WebSocket, and beacon calls. Raw transports are allowed only under `src/api`. The fixed chat baseline and the last generated results live in `payload-baseline.json` and `payload-final.json`; `payload-report.md` explains inclusion rules and deltas. Those results are a historical record; the measurement script that produced them has been removed.

## Lint configuration

`.oxlintrc.json` explicitly maps the previous ESLint and typescript-eslint
recommended rules, plus Rules of Hooks and exhaustive dependency checks, to native
Oxlint rules. Default rule categories are disabled to avoid silently adding unrelated
rules. TypeScript handles compiler diagnostics such as undefined names in TS/TSX;
JavaScript files retain the corresponding lint checks. Legacy octal literals are
rejected by the module parser rather than a separate `no-octal` rule.

Unused parameters and caught errors prefixed with `_` remain allowed, as does
explicit `any`. Mount-lifetime Hooks exceptions remain local and documented.
`no-useless-assignment` is disabled only in the live and shell stores: Oxlint
1.91.0 incorrectly reports their retry-backoff assignments as unused even though
later iterations consume them when a connection fails before the reset. Remove
those overrides when the loop analysis is fixed.

## Build contract

- The existing chat build runs first with `emptyOutDir: true`; `vite.hub.config.ts` then writes into the same directory with `emptyOutDir: false`.
- The chat keeps its relative Vite base. Entry JS/CSS (`dist/app-[hash].js`/`.css`), chunks (`dist/chunks/[name]-[hash].js`), lazy CSS, fonts, and other assets all use content-derived names. CSS grouping is selected before hashing/preload generation, with no post-hash byte rewrites. `dist/asset-manifest.json` is Vite's build manifest; Go reads it to render the entry, classify immutable assets, prewarm chunks, and inject the complete service-worker asset list.
- The standalone Hub build emits exactly `dist/hub.js` and `dist/hub.css`, with Preact and Signals bundled into `hub.js` and no chat-only dependencies or dynamic chunks.
- Hub assets are intentionally absent from the chat service worker. Only the exact two Hub assets are public before Hub authentication.
- KaTeX and highlight.js remain deterministic chat-only lazy chunks. KaTeX emits only WOFF2 fonts.
- Source maps and frontend test artifacts are not emitted under `static`; the generated chat asset manifest is embedded.
- `build:compress` runs last (`go run ../cmd/precompressui`): every text output that gzip shrinks is replaced in place by a deterministic, level-9 `X.gz`. Go code always uses the logical name `X`; the server sends stored gzip bytes unchanged to gzip clients and decodes them only for clients that refuse gzip. `StaticAsset` returns decoded content, and the asset versions hash the stored bytes. Fonts and images stay raw.
- `AssetVersion()` covers only chat assets. `HubAssetVersion()` covers exactly `hub.js` and `hub.css`; both Hub JS and CSS use `?v=<HubAssetVersion>` in the shell and are immutable only on an exact version match.

The payload baseline's historical `app-interject.js` entry describes a previous measured asset set; it is not an active chunk name. The current Vite build emits a hashed entry and named hashed chunks, including lazy steering actions and queue presentation. Do not rename baseline entries by guessing or relax its budget to mask growth.

Do not edit generated `static/dist` files directly. Change TypeScript/CSS and run `make frontend`; only source and package manifests are committed.

## Standalone proxy administration

`frontend/proxy/` owns the plain TypeScript DOM UI; transport belongs in
`src/api/proxy-client.ts`. It uses no Preact, chat modules, inline scripts or HTML
insertion: API content is rendered as text. The backend shell at `/_proxy/`
provides `#proxy-app` and loads `/_proxy/assets/proxy.js` and `proxy.css`.
`vite.proxy.config.ts` emits exactly these two assets without clearing chat/Hub
outputs. The normal build typechecks and builds proxy assets before precompression.

Proxy assets stay outside the chat asset version, hashed manifest, service worker
and authoring asset list. Stable proxy URLs require revalidation, not immutable
caching. Do not register a proxy service worker.

The UI calls token-bearing applications/devices **Clients**. Keep the `callers`
API endpoints, JSON fields and `caller_auth` configuration compatible. Legacy
`?tab=callers` links open Clients and normalize to `?tab=clients`.

`/_proxy/api/state` is authoritative for CSRF, revision and mode. Mutations use
`X-CSRF-Token` and same-origin session credentials. Configuration writes submit the loaded revision; conflicts require explicit refresh.
File-managed routes and file-sourced secrets are read-only, with explicit file
reload; stored secrets remain rotatable. The UI cannot edit file references.
The **Admin** tab owns secret-free route download and confirmed YAML replacement
(file up to 1 MiB, or select **Paste YAML**). Configuration controls are directly
visible, without nested disclosures. The server parses YAML; this is route
configuration transfer, not a full backup.

The Providers page uses stored provider IDs, families, status and per-route
capabilities from state. Family sections paginate independently. **Client
defaults** saves route names for search, fetch, image, audio, music and
transcription with `PUT /_proxy/api/defaults`. These defaults apply only to
clients with access to the selected route and no explicit local selector.
Embeddings deliberately have no proxy default.

The **Add provider** picker groups presets by family. Inference names can be
edited at creation; other families use canonical names. Key-backed presets send
one new key or an existing secret name to `POST /_proxy/api/providers`; the
server creates the provider, its routes and any new secret together. For an
advanced route edit, **Add API key** opens the Secrets form without discarding
the draft. Those secret and route writes are separate. Route saves do not probe
an upstream.

Subpath rules let one route, one client token and one grant serve several
upstream roots. Each rule declares its own upstream, authentication and optional
allowlist, with no parent substitution. OpenCode Go uses `/`, `/messages/` and
`/gemini/`. `METHOD /prefix/*` allows that method on any safe path below the
prefix; `GET /*` allows GET on any safe path. The route editor keeps the exact
syntax; Manage displays it in prose. Adding a missing companion never grants
client access automatically.

Secrets are write-only and referenced by name, including across upstreams.
The Secrets page shows the source and the routes using each key. Duplicate
names require explicit Replace with `replace: true`. Literal header values are
public configuration, not a credential store. Administrator tokens, secret
values and client tokens are never persisted in browser storage; server-stored
secrets are plaintext at rest.

**Providers → Add provider** uses shared subscriptions for Claude Code and
personal Antigravity. **Create shared route & sign in** posts
`{ revision, shared_action: "create", caller | name }` to
`/_proxy/api/claude/setup` or `/_proxy/api/agy/setup`. **Manage → Sign in to
shared subscription** or **Reconnect** posts
`{ revision, route, shared_action: "connect", caller | name }` for an existing
shared route. Sign-in always runs from a real client: `caller` arms an existing
active client, which keeps its token and other approvals, gains the route grant
and gets a 15-minute enrollment; the reply omits `token`, and the command uses
`"$TERM_LLM_PROXY_TOKEN"` in its place. `name` creates a new client instead,
whose token is shown once. No separate launcher client is created. The operator
signs in once and refreshes the UI. Only the Antigravity operator command
appends `-- acp --login`.

**Manage → Add device** posts `{ revision, route }` to issue a separately
revocable worker launcher, never to retrieve an existing token. It is available
only for enabled `shared_vault` routes in state `ready`. Workers do not sign in
or receive vendor credentials. They can copy optional `providers.claude-bin.proxy`
or `providers.agy-bin.proxy` YAML; operator launchers never show worker YAML.
Responses `{ url, token, caller_id, revision }` are shown once with a safely
quoted command and are not persisted in browser storage or page URLs.

Schema v8 preserves routes, client tokens, grants and existing shared Claude
sign-ins. It converts per-device accounts to `shared_vault` with state
`needs_login`; the operator must sign in again once for each converted account.
The UI does not offer per-device sign-in and keeps `needs_login` and `refreshing`
routes out of new worker access. Existing clients can still be managed.
**Manage → Delete provider** uses `DELETE /_proxy/api/providers/{name}` with
`{ revision }`. It removes the provider, its routes and their grants and
defaults, plus secrets no remaining route references. Accounts stay until
removed separately. Leftover native sign-ins retain **Clear all … setup**.
File-managed providers and defaults are read-only. Canonical Claude launchers
and worker YAML use `/claude-bin/`; custom mounts use the saved route prefix.

Native routes are recognized by their account kind, not their name or URL. The
native presets use guided setup instead of the generic account editor. There is
no browser OAuth wizard or token paste for native setup. **Clients** lists
scoped clients and grants; ready native routes use worker launcher examples,
not API-key curl commands.

Other provider accounts retain write-only credential JSON and explicit replacement
with fresh passkey authentication. **Manage** opens metadata and sign-out without
nested disclosures. Generic route forms keep billing choices short and hide
transport details and previews under **Advanced**; empty header-substitution
controls are absent on account-backed routes.

Client grants start unchecked and create/edit submits the state revision. Grant
edits do not reissue tokens. Tokens are shown once with a Copy action; curl examples
use `$TOKEN`. Tabs, refresh and sign-out confirm dismissal while a token is shown;
browser leave requests a native warning, but shutdown can still discard it.

In **Clients → Revoked**, permanently delete a single revoked client or select
**Delete all revoked**. Both actions require confirmation and keep request and
spending history. `DELETE /_proxy/api/callers` with `{ "id": "…" }` still revokes
without deleting. Send `{ "id": "…", "purge": true }` to delete one revoked
client (`409` for an active client, `404` if missing), or
`{ "purge_revoked": true }` without an ID to delete all revoked clients. A
successful delete returns `{ "ok": true }`.

Upstream origin/base-path or canonical substitution changes invalidate grants;
local-prefix, enabled and local-auth-format changes and same-name secret rotation
preserve them. File configuration changes drop incompatible grants fail-closed.
Warn before authority-bearing edits. Revoke, delete and disable block new requests,
not requests already dispatched.

Requests use a compact aligned ledger: time, status, model, client, total input,
output, cache-read share, duration and price. Local calendar-day headings and
24-hour times replace repeated full timestamps. Responsive density decreases progressively: 833–1024px combines time/status while
retaining client/cache; 641–832px places inline client/route beneath the model; at 640px and below a
compact wrapping facts group keeps output/thinking together. Full paths, raw
counters, IDs, bytes and first-byte timing stay in expandable details. Total input includes cache components for Messages/Bedrock, but never
adds them again to inclusive OpenAI/Gemini input. Unknown counters/prices are not
zero. Plan API-equivalent estimates remain distinct from actual spending.

With `usage: auto`, supported HTTP JSON inference bodies are passively observed
under a 1 MiB/depth-64 budget. Expanding a row immediately shows **Requested**
settings, independent **Response** facts and aligned metadata below a divider;
there are no nested settings/technical disclosures. Legacy, unsupported, invalid
and truncated request settings are not provider defaults. Explicit zero/false
remain values, and documented dynamic/unlimited budgets are labeled as such.
Only positive trustworthy thinking counts earn a summary subline (`5.1k think`,
never a plus or unknown/zero placeholder). OpenAI thinking is included in output;
Gemini thoughts are additional, as indicated by detail labels/tooltips. Effort
summaries are values alone. Finite finish reasons and bounded mixed candidates
never replace transport errors/interruption with a successful completion label.
The request probe retains no prompts, tool identities/schemas, cache keys or
provider IDs, and never affects usage accounting or frozen prices. WebSocket
request-setting correlation, true TTFT and output rates remain deferred. **First
byte** is response-byte timing, never TTFT. Raw capture keeps its separate explicit
access and security controls.

Only consecutive identical HTTP errors without measured usage or prices collapse
into expandable groups, within the loaded page. Group time/latency describe the
latest request, not sums; pagination continues to count individual requests.
Filters stay collapsed initially. **Apply filters** commits the draft on a successful
load. The badge, pagination, **Refresh**, and metadata export use the applied query;
**Clear** resets the fields and results. Numbered pages contain 25 requests; controls
are in the footer, with a quiet result count above. Older-sequence cursors remain
a legacy API option. Authenticated JSON export is bounded to 1,000 rows.

Normal request history/export excludes bodies and headers. Expanding one request checks
that request's capture metadata by exact ID; **View raw payload** opens the shared,
bounded Debug preview inline without leaving the list. Never fetch captured bytes
merely to render a ledger row. Errors/incomplete results can still mean upstream
work happened; do not suggest automatic retries.

The **Debug** tab explicitly enables/disables raw payload capture for the running
process, with a sensitive-data confirmation before enabling. The fixed private
archive defaults to the proxy state directory's `debug-payloads`; a CLI directory
flag overrides it and enables startup capture. Admin-only APIs list captures and
return bounded UTF-8/base64 previews, with offset paging for large files. Render
payloads only as text, never HTML/markdown; do not persist them in browser storage,
URLs, caches or the service worker. Navigation/sign-out disposes preview content
and ignores stale responses. Debug inspection is not an arbitrary file browser;
only capture manifests and body/frame files are supported.

Only curated JSON `{error: string}` responses for 400/409 are shown as text,
limited to 512 characters. Error reads are bounded; other responses use generic
messages rather than displaying arbitrary bodies.

Run the standalone coverage and checks from the repository root:

```sh
(cd frontend && npx vitest run --config vite.proxy.config.ts)
npm --prefix frontend run typecheck
(cd frontend && npx stylelint 'proxy/**/*.css')
make test PKG=./internal/serveui
```

For browser layout coverage without changing embedded build artifacts, build the
standalone bundle into scratch space and point Playwright at it:

```sh
(cd frontend && npx vite build --config vite.proxy.config.ts --outDir ../tmp/proxy-native-dist)
(cd frontend && TERM_LLM_PROXY_DIST="$PWD/../tmp/proxy-native-dist" npx playwright test e2e/proxy-layout.spec.ts e2e/proxy-request-insights.spec.ts --project=desktop --project=mobile)
```

The separate Vite test configuration covers `proxy/`; the chat suite covers
`src/`. `npm test` runs both, and standard typechecking/CSS lint include `proxy/`.
Check keyboard-only sign-in, route editing, show-once token dismissal, request
filters/details and narrow layouts. For external passkey browser coverage, see
[`scripts/proxy_passkey_smoke.sh --help`](../scripts/proxy_passkey_smoke.sh).

## Static caching and deployments

| URL class                                              | HTTP Cache-Control                    | Chat service worker                                             |
| ------------------------------------------------------ | ------------------------------------- | --------------------------------------------------------------- |
| Manifest-listed hashed chat dist assets (any query)    | `public, max-age=31536000, immutable` | Cache-first in scope-specific `term-llm-assets-*`               |
| Icon/web manifest with exact current `?v=`             | `public, max-age=31536000, immutable` | Stale-while-revalidate in version-scoped shell cache            |
| Other embedded non-hashed assets with exact chat `?v=` | `public, max-age=31536000, immutable` | Network-first for scripts/styles/images/fonts; otherwise bypass |
| Other non-hashed assets; missing/mismatched `?v=`      | `no-cache` + ETag                     | Scripts/styles/images/fonts network-first, otherwise bypass     |
| `sw.js` (any query)                                    | `no-cache` + ETag                     | Browser worker update handling                                  |
| Chat navigation/`index.html` (any query)               | `no-cache, no-store, must-revalidate` | Bypass: authoritative network shell                             |
| Hub JS/CSS with exact Hub `?v=`                        | `public, max-age=31536000, immutable` | Bypass                                                          |
| Hub JS/CSS without exact Hub version                   | `no-cache` + ETag                     | Bypass                                                          |
| Hub shell                                              | `no-store`                            | Bypass                                                          |
| Extension assets under the current generation          | `private, no-cache` + ETag            | Bypass                                                          |
| APIs, auth, extension status                           | Existing policies, unchanged          | Bypass                                                          |

Immutable is safe because each chat URL identifies build content, including its
hashed dependency references. All imports/preloads belong to one build graph;
lazy chunks import the same hashed entry URL as the HTML (no query-suffixed
second module identity). The server never substitutes new bytes at an old hashed
URL: missing chunks return 404. Relative paths and `<base>` preserve subpath and
Hub-node proxy mounts. Ordinary HTTP caching avoids per-module conditional
revalidation on cached revisits even without an active service worker, including
WKWebView; the HTML remains authoritative on every navigation.

Service-worker activation drops obsolete version-scoped shell caches but retains
cached hashed files referenced by the current or immediately previous deployment.
Scope-specific metadata records the previous list; repeating activation of the
same build does not advance that window. This supports already-open clients for
one deployment **only for files already cached**. Uncached old chunks can return
404 and lazy UI failures offer a reload. No eager precache, API persistence, or
unbounded historical asset retention is introduced. Failed/auth-redirected
responses are never stored as immutable assets. Version-mismatch hard refresh
clears shell caches and updates the worker, leaving safe content-addressed files
for activation to prune. Cache eviction/storage denial can still require network
fetches; this is not an offline application.

## Startup caches (fast reopen)

Chat startup has three milestones, recorded as `performance.mark('term-llm:<name>')`
and in `AppStore.startupMetrics` (timings and counters only, never transcript text):

1. `firstUsefulPaint` — the shell shows a restored last-known workspace, or the fresh
   sidebar with the selected conversation hydrating.
2. `authoritative` — the selected conversation (or new chat) is confirmed by the server.
3. `actionsReady` — `startupDone`; lifecycle, server events and sending are enabled.

Model discovery, skills, branches and Hub agent health never block 1 or 2. The event
feed gets a 150 ms head start; if its cursor arrives later, one catch-up sidebar/status
reconciliation covers the gap. Background 401/403s fail the running bootstrap or
return the user to the credential gate; they are never swallowed.

Persistent caches live in IndexedDB (`term_llm_ui_cache`, `platform/persistent-cache.ts`)
and are display hints only. Every record is reconciled by the normal authoritative
reads; authorization, liveness, pending approvals/questions, and provider
availability are always server-decided.

| Record (per scope)                  | Bound                                              | Freshness                                                                                      |
| ----------------------------------- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Chat sidebar/projects (first page)  | 1 record, ≤ 800 KB                                 | shown ≤ 14 days old; replaced wholesale by the first server snapshot                           |
| Recently viewed conversations       | LRU of 8, ≤ 700 KB each, ≤ 4 MB total, ≤ 80 msgs   | shown ≤ 14 days old; hydration replaces the bodies (revision-checked)                          |
| Provider list, model catalogs       | ≤ 12 records, ≤ 2 MB                               | shown ≤ 7 days old; models refetched at startup when > 10 min old; provider change invalidates |
| Hub nodes / attention / delegations | 3 records, ≤ 128 KiB each; ≤ 200 nodes, ≤ 50 items | shown ≤ 7 days old; each section replaced as its request lands                                 |

**Scope and privacy.** Keys combine origin, UI prefix, Hub node id and a server-injected
opaque `TERM_LLM_CACHE_SCOPE` (chat: SHA-256 of the per-database random
`store_instance_id` and auth mode; Hub: its random passkey user handle or state identity).
Credentials are never key material. Without a scope, private persistence is disabled.
Cached private content is displayed before an API response only when the HTML
navigation itself required authorization (passkey, Hub-proxied nodes, Hub dashboard) or
the server runs without auth. Bearer-token chat servers serve public HTML, so their
cache waits for the first authenticated response. Any 401/403 purges the scope; a
token change purges it; Hub logout deletes the whole database; Settings → Connection
offers "Clear cached data on this device".

At rest, records are plain IndexedDB data in the browser profile. That matches the
existing boundary: bearer mode already keeps the server token in `localStorage` of the
same profile, and passkey sessions live in its cookie jar, so anyone with profile
access already has API access. The injected scope is an opaque hash and grants
nothing by itself. The Hub scope is account-scoped (passkey user handle, or a random
sidecar identity); Hub content comes from live nodes, so a replaced node list is
reconciled on the first nodes response and removed nodes' summaries are purged.

**What is never cached:** tokens/cookies, auth or permission decisions, approvals and
ask-user prompts, live stream state (`activeRun`, `activeResponseId`), history paging
anchors, the installed-bodies revision, attachment bytes, `data:`/`blob:` URLs, and
signed URLs. Restored rows cannot resume a stream, acknowledge attention, or page older
history until the server confirms them. Routed launches only restore the exact routed
conversation; `?new=1` and active drafts restore as a new chat. A conversation that no
longer resolves (404/410 or `selected_session: null`) is forgotten locally.

**Schema changes.** Bump `WORKSPACE_CACHE_SCHEMA` / `HUB_CACHE_SCHEMA` when a persisted
shape changes; mismatched records are ignored and replaced. The IndexedDB version
upgrade drops the store, since every record is disposable.

## Storage migration and rollback

Drafts, queued review comments, pending intents, and attention markers are independently keyed
records. New readers retain compatibility reads for the previous aggregate format, but all new
writes use record keys. Rollback must **not** delete or rewrite the new record keys. An older build
may continue reading its aggregate data while a subsequent upgrade resumes additive migration.
Deletion tombstones must also be retained during that compatibility window so deleted legacy
review comments and drafts are not resurrected.

## Accessibility smoke checklist

Run this checklist on a representative desktop and mobile build after interaction changes:

- **NVDA/Firefox or Chrome:** dialog names are announced; Tab and Shift+Tab remain trapped;
  Escape follows the documented neutral-dismiss policy; focus returns to the opener.
- **VoiceOver/Safari:** mobile sidebar, diff drawer, plan sheet, and run center announce their
  labels and expanded state; background chat controls cannot be activated while open.
- **TalkBack/Chrome:** drawer actions remain above the visual keyboard and safe-area inset;
  explicit close controls remain reachable; menus announce menu items and support sequential
  navigation.
- **Media:** closing a lightbox pauses video, clears playback, restores focus, and revokes only
  object URLs owned by the lightbox.
- **Nested surfaces:** opening an approval or media surface above another overlay makes the lower
  surface inert until the top surface closes.
