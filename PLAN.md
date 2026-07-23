# PLAN: Post-stream transcript sync via a revisioned identity index

One PR. Server-authoritative transcript synchronization for the serve web UI,
replacing timestamp-based cross-tab reconciliation and heuristic post-stream
merging with a durable per-session `transcript_rev` plus a compact identity
index, and bounding client memory/DOM with turn-segment eviction.

---

## 1. Problem

The web UI (`internal/serveui/static/`) keeps each session's full transcript as
a `session.messages` array in memory and localStorage, and reconciles it with
the server through two fragile mechanisms:

1. **Change detection by wall clock.** `/v1/sessions/status` exposes
   `transcript_updated_at`, explicitly documented as "backed by
   `SessionSummary.UpdatedAt` because all message writes bump
   `sessions.updated_at`" (`cmd/serve_handlers_status.go:46`). Any metadata
   write that bumps `updated_at` looks like a transcript change; same-ms writes
   are invisible; clients maintain a "catch-up warning" state machine
   (`showActiveTranscriptCatchUpWarning`, `reconcilePendingActiveTranscriptRefresh`,
   `app-sessions.js:180-330`) to paper over missed/duplicate signals.
2. **Identity by heuristic.** After a stream ends (or another tab streamed),
   the client fetches `/v1/sessions/{id}/messages?tail=1` and merges it into
   local state with content heuristics (`mergeServerMessagesWithLocalState`,
   `localToolGroupCoveredByServerTurn`, synthetic ask-user rows —
   `app-sessions.js:1484-1665`). Streamed tool groups vs. stored rows are
   matched by tool-call ids and turn shape; mismatches duplicate or drop rows.

Secondary problem: unbounded growth. Long sessions keep every message body in
JS memory, in localStorage, and as live DOM. Inactive sessions retain full
transcripts.

**Goal:** every durable transcript state has a single integer version; clients
sync by (rev, seq) identity, not timestamps or content matching; client keeps a
compact ID skeleton plus a bounded set of materialized turn segments.

## 2. Invariants (hard, test-enforced)

- **I0 Complete identity stream.** For the selected session the client knows the
  complete authoritative ordered identity of every durable display row. A body
  may be unloaded, but an unloaded interval is explicit and can never appear as
  adjacent transcript content.
- **I1 Monotonic rev.** Every committed mutation of a session's `messages`
  rows increments `sessions.transcript_rev` by ≥1 in the *same* SQLite
  transaction. No transcript write path exists outside this rule.
- **I2 Coherent reads.** Any API response carrying `rev` computed it in the
  same read transaction as the rows it returns. A client can never observe
  index items paired with a rev that does not describe them.
- **I3 Durable identity and order.** `messages.id` is the stable identity of a
  durable row; `messages.sequence` is its current per-session ordering key.
  Rewrites may retire row IDs, preserve surviving IDs, or reassign sequences,
  but always bump rev. Clients diff by row ID and derive position only from the
  latest index; sequence is never used as identity.
- **I4 Attachability.** For an active run, `started_rev` is recorded at run
  registration, *before* the engine persists the triggering user row. Any
  client whose skeleton is reconciled to ≥ `started_rev` may attach; rows
  persisted during the run always have rev > `started_rev` and are reconciled
  by index delta at run end.
- **I5 Bounded client.** Materialized bodies/DOM never exceed the configured
  turn budget (except when the pinned set alone exceeds it); eviction removes
  whole turn segments only; the visible window ± overscan and the active
  optimistic tail are never evicted.
- **I6 No lost optimistic work.** A locally streamed turn is only replaced by
  durable rows when the index proves the server persisted that turn
  (rev advanced AND matching rows present). Stop-before-flush keeps local
  rows, exactly as today.

## 3. Current touchpoints

Server (Go):
- `internal/session/sqlite.go`
  - schema: `sessions` (has `compaction_seq`, `compaction_count`), `messages`
    (`id`, `session_id`, `role`, `parts`, `turn_index`, `sequence`,
    `compaction_tail`) — lines 46–183; migration framework with numbered
    `migrations` entries (~line 300+).
  - write paths: `AddMessage` → `addMessageExplicitSequence` /
    `addMessageAutoSequence` → `insertMessageAndBumpSession` (2419–2540);
    `ReplaceMessages` (2676), `ReplaceCompactedMessages` (2747),
    `CompactMessages` (3031), `ClearCompactionBoundary` (2657). All already run
    in transactions (`retryOnBusy` + `BeginTx`/`BEGIN IMMEDIATE`).
- `cmd/serve_handlers.go` — `handleSessionByID` routes `/v1/sessions/{id}/...`
  (1355); `messages` endpoint with `tail`/`before_seq`/`offset` (1548–1624);
  `sessionMessageEntries` visibility filter (1222–1335): system/developer rows
  hidden, tool results only surfaced on error/images/plan;
  `getSessionMessagesPageDescending` (1193).
- `cmd/serve_handlers_status.go` — `/v1/sessions/status`,
  `transcript_updated_at` (13–78).
- `cmd/serve_response_runs.go` — `responseRunManager` (`runs`,
  `activeBySession`), per-run SSE event `Sequence`, recovery snapshots.
- `cmd/serve_ui_stream.go` — `streamUIResponses` → `startResponseRun` happens
  before any engine persistence of the user row.
- `cmd/serve.go:1220-1233` — route registration.

Client (JS, `internal/serveui/static/`):
- `app-sessions.js` — session objects (`messages`, `lastSequenceNumber`,
  `lastResponseId`, `activeResponseId`, `_serverOnly`), tail merge
  (`applyTailMessagesToSessionHistory` 1124, `mergeServerMessagesWithLocalState`
  1613), compaction scrollback preservation (~1114), timestamp reconciliation
  (180–330, 1667–1835), snapshot-recovery gate (37–44, 2033–2036).
- `app-stream.js` — streaming events, optimistic rendering, post-stream refresh.
- `app-core.js`, `app-render.js` — transcript DOM rendering.
- `sw.js` — cache-first shell (`SHELL_CACHE = 'term-llm-shell-v2'`,
  `SHELL_ASSETS`); cached JS can lag the server by one deploy.
- `index.html`, `internal/serveui/embed.go` (`RenderIndexHTML`,
  `RenderServiceWorker` replacement tables), `embed_test.go` (Node test
  runner wiring).

## 4. Design overview

- Add `sessions.transcript_rev` bumped transactionally by every messages-table
  write.
- New endpoints: `GET /v1/sessions/{id}/transcript` (compact identity index)
  and `GET /v1/sessions/{id}/transcript/bodies` (bodies for explicit seqs).
  `/v1/sessions/status` additionally reports `transcript_rev`.
- Client keeps, for the active session: `rev`, a compact parallel-array ID
  skeleton (`ids`, `seqs`, role-code string, flags), a bounded body cache keyed
  by durable row ID, and an optimistic tail for the active run. Rendering
  materializes turn segments on demand; eviction drops whole segments behind
  spacers.
- Post-stream and cross-tab reconciliation become the same operation: fetch
  index at ≥ local rev, diff by seq, fetch missing visible bodies, swap
  optimistic rows for durable ones.
- The old timestamp catch-up machinery and content-heuristic merge are deleted.
  A single thin fallback remains for service-worker skew (see §11).

## 5. Schema & store changes (`internal/session/`)

### 5.1 Migration

New numbered migration:

```sql
ALTER TABLE sessions ADD COLUMN transcript_rev INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET transcript_rev = message_count; -- cheap non-zero seed
```

Seed value is irrelevant to correctness (only monotonicity matters); seeding
from `message_count` just makes fresh revs non-zero for populated sessions.
Follow the existing `hasXxx` column-presence pattern (`s.hasCompactionSeq`
etc.) for read-only/old DBs: absent column ⇒ store reports rev 0 and the serve
layer treats the session as unversioned (client falls back, §11).

### 5.2 Write paths

Single helper, called inside the existing transactions:

```go
func bumpTranscriptRev(ctx, execer, sessionID) (int64, error) // UPDATE ... SET transcript_rev = transcript_rev + 1 ... RETURNING transcript_rev
```

Wire into:
- `insertMessageAndBumpSession` (covers both AddMessage paths),
- `ReplaceMessages`, `ReplaceCompactedMessages`, `CompactMessages`,
- `ClearCompactionBoundary` (changes what the index reports),
- any other statement touching `messages` — do a repo-wide audit
  (`grep -n 'messages' internal/session/sqlite.go` for INSERT/UPDATE/DELETE)
  and add a Go test that fails if a store write method mutates `messages`
  without bumping rev (call each `Store` write method against a probe DB and
  assert rev strictly increased).

All these paths already run in `BeginTx`/`BEGIN IMMEDIATE` transactions, so
the bump is implementable without restructuring; `retryOnBusy` retries the
whole transaction, keeping I1 under contention.

### 5.3 New store methods (optional interface, like `MessagesDescendingPager`)

```go
type TranscriptIndexer interface {
    // GetTranscriptSnapshot returns rev + compaction envelope + every index row
    // (role NOT IN ('system','developer')), ordered by sequence, in one read
    // transaction. GetTranscriptIndex is a convenience projection used by
    // non-HTTP callers.
    GetTranscriptSnapshot(ctx, sessionID string) (TranscriptSnapshot, error)
    GetTranscriptIndex(ctx, sessionID string) (rev int64, items []TranscriptIndexItem, err error)
    // GetMessagesByIDs returns rev + full rows for the durable row IDs, one transaction.
    GetMessagesByIDs(ctx, sessionID string, ids []int64) (rev int64, msgs []Message, err error)
    TranscriptRev(ctx, sessionID string) (int64, error)
}

type TranscriptVersionReporter interface {
    // False only for an older database opened read-only without the migration;
    // the serve layer then exposes the documented /messages fallback instead.
    TranscriptVersioned() bool
}

type TranscriptSnapshot struct {
    Rev int64
    CompactionSeq, CompactionCount int
    Items []TranscriptIndexItem
}

type TranscriptIndexItem struct {
    Seq       int
    ID        int64
    Role      string // user|assistant|tool|event
    Flags     uint8  // bit 0: compaction_tail; bit 1: empty_body (renders to no parts)
}
```

Both read methods open one read transaction, `SELECT transcript_rev`, then the
rows, then commit — SQLite WAL snapshot isolation guarantees I2. Index query
uses existing `idx_messages_session_id (session_id, sequence)`.

**Exactly which rows enter the index:** every `messages` row for the session
whose role is not `system`/`developer` — including `event` rows, tool rows,
and rows whose rendered body is empty under `sessionMessageEntries` filtering.
Empty-rendering rows get `empty_body` so clients keep their identity (for
grouping and rev/diff correctness) without fetching bodies. Pre-compaction
rows are included (scrollback); `compaction_seq`/`compaction_count` ride along
in the response envelope.

**Grouping is index-only.** A *turn segment* is: one `user` row plus all
following non-`user` rows up to (excluding) the next `user` row; a leading
run of non-user rows forms segment 0. Segments are computed purely from
skeleton roles+seqs, so grouping is identical whether or not bodies are
materialized. (`turn_index` is not used: it defaults to 0 on old rows.)

## 6. HTTP API (`cmd/`)

Routed from `handleSessionByID` alongside the existing `messages` suffix.

### 6.1 `GET /v1/sessions/{id}/transcript`

```json
{
  "rev": 42,
  "compaction_seq": 17, "compaction_count": 2,
  "rows": {
    "seqs":  [0, 1, 3, 4],
    "ids":   [11, 12, 15, 16],
    "roles": "uaea",
    "flags": [0, 0, 0, 1]
  }
}
```

- The response contains every index row. It does not silently truncate: doing
  so would reintroduce an unknown prefix and violate I1. The representation is
  compact parallel arrays rather than one JSON object per row. Gzip + ETag use
  the existing conditional-response helpers. A future protocol may page index
  *chunks* only if it also carries explicit total ordinals/chunk coverage; that
  complexity is outside this PR.
- The client stores the arrays compactly and only for the selected/recent
  session. Bodies and DOM—not the ID skeleton—are the primary memory cost and
  are strictly bounded.
- No `since_rev` delta on the wire: full refetch plus ETag/304 is deliberately
  simpler for this PR. The client diffs locally by durable row ID.

### 6.2 `GET /v1/sessions/{id}/transcript/bodies?ids=11,12,15-20`

- `ids`: comma list with ranges; expanded count capped at
  `transcriptBodiesMaxIDs = 512` and rejected with HTTP 400 beyond the cap.
- Response: `{"rev": 42, "messages": [sessionMessageEntry...]}` — entries in
  the existing `sessionMessageEntry` shape. The server expands requested IDs to
  complete turn segments where needed so tool-call/result/error grouping stays
  coherent. Unknown/retired IDs are omitted; the client treats that as a stale
  index and refreshes it before retrying once.
- Body identity is the durable row ID. Sequence is returned for ordering and
  validation but is never the body-cache key.

### 6.3 Status & runs

- `/v1/sessions/status` entries gain `"transcript_rev"`. Keep
  `transcript_updated_at` in the payload — the hub dashboard and older cached
  UI still read it (documented as compatibility, not a client correctness
  input anymore).
- `startResponseRun` records `startedRev` (via `TranscriptRev`) on the
  `responseRun` at registration. The active run's `response_id` and
  `started_rev` are exposed together by `/v1/sessions/{id}/state` and the
  transcript envelope, and are also exposed:
  - in the SSE `response.created`/snapshot recovery payload as `started_rev`;
  - in the terminal events (`response.completed`/`failed`/`cancelled`) as
    `final_rev`, read *after* the engine's last persistence for the run
    (the run already knows when its final flush finished).
  Ordering guarantee (matches actual persistence order in
  `streamUIResponses` → engine): `startedRev` ≤ rev of the triggering user
  row ≤ `final_rev`. The client precondition to attach is only
  `localRev ≥ started_rev` — always satisfiable from a fresh index fetch,
  never referencing rows persisted later in the run.

## 7. Client structures & algorithms (`internal/serveui/static/`)

New file `transcript-store.js` (pure logic, no DOM — loaded before
`app-sessions.js`; testable under Node like `markdown-setup.js`).

### 7.1 State per session

```js
session.transcript = {
  rev: 0,
  // Compact parallel arrays in authoritative order. Implementations may use
  // chunked typed arrays internally; do not allocate one object per index row.
  ids: [], seqs: [], roles: '', flags: [],
  compactionSeq: -1, compactionCount: 0,
  bodies: Map<rowId, entry>,              // bounded materialized durable rows
  segments: [ {startOrdinal, endOrdinal, state: 'evicted'|'materialized', estHeight} ],
  optimistic: [ localEntry... ],          // pinned active/local overlay
  activeRun: { id, startedRev } | null,
}
```

localStorage persists existing metadata/drafts plus a strictly bounded active
optimistic overlay needed to survive reload during a run; it never persists the
durable skeleton or durable bodies. Deselecting a session releases bodies/DOM
immediately. Skeletons may be retained for a small recent-session LRU and are
otherwise released with their ETag, so reactivation performs a full index fetch
rather than accepting a meaningless 304 without a local index.

### 7.2 Budgets (single config object, defaults + override)

```js
const TRANSCRIPT_BUDGETS = Object.assign({
  maxMaterializedTurns: 60,  // durable bodies + DOM turn containers
  overscanTurns: 8,          // pinned around viewport
  maxRecentSkeletons: 2,     // active plus one recently visited session
}, window.TERM_LLM_TRANSCRIPT_BUDGETS || {});
```

Turn-count budgets, not bytes: they are observable and synchronously
enforceable in tests. Hard invariants I5 are asserted by the store after every
mutation in dev/test builds (`transcriptStore._checkInvariants()` invoked by
the Node tests; no-op in production path).

### 7.3 Sync algorithm (one function, all triggers)

`syncTranscript(sessionId, {reason})` — triggered by: session activation,
status poll showing `transcript_rev > local rev`, stream terminal event
carrying `final_rev > local rev`, scroll into an evicted/unfetched region.

1. `GET /transcript` (If-None-Match). 304 ⇒ done.
2. Diff server order against the local index by durable row ID while accepting
   updated sequence/order attributes from the server:
   - identical ordered-ID prefix plus appended IDs ⇒ append; mark new segments
     evicted; fetch bodies only for pinned viewport/tail segments.
   - divergence in ordered IDs, changed sequence attributes, compaction change,
     or shrink ⇒ **rewrite** from the first divergent ordinal: retire bodies for
     IDs no longer present, keep bodies for surviving IDs, adopt server order,
     and refetch visible bodies as needed. Pre-divergence scrollback is untouched.
3. Reconcile optimistic tail (§8).
4. `rev = response.rev`; enforce budgets (evict farthest unpinned segments).

Body fetches always request whole turn segments (`seqs=start-end`), so a
segment is either fully materialized or fully evicted — no partial groups.

### 7.4 Rendering & DOM eviction (`app-render.js` / `app-core.js`)

- Each turn segment renders into one container element. Evicted segments are a
  single spacer `div` with `height: estHeight` (recorded when last
  materialized; default estimate otherwise) so scrollbar geometry is stable.
- IntersectionObserver on spacers triggers `syncTranscript(...,{reason:'scroll'})`
  → bodies fetch → replace spacer with rendered segment; segments scrolled far
  out of the window are demoted back to spacers when over budget.
- DOM nodes for transcript rows exist iff their segment is materialized
  (DOM ⊆ bodies, so I5 bounds both).
- Every structural render captures the first visible durable row ID and its
  top offset relative to the scrollport, applies the model/DOM change, then
  adjusts `scrollTop` so the same surviving ID returns to the same pixel. If
  that ID was retired by a rewrite, use the nearest surviving predecessor,
  otherwise successor. The active viewport segment is pinned throughout.
- No individual DOM placeholder is created for every absent body. Each
  contiguous evicted ordinal run is represented by one spacer/gap container.
  Thus a 100k-row skeleton can still have O(materialized turns + gaps) DOM.

## 8. Active-run & optimistic semantics

Send path (`app-stream.js`):
1. User submits ⇒ push optimistic user entry (clientKey) onto
  `transcript.optimistic`; record `revAtSend = transcript.rev`.
2. `x-response-id` header ⇒ `activeRun = {id, startedRev}` from
   `response.created`. If `startedRev > rev`, run `syncTranscript` once
   (I4 guarantees this converges without waiting for in-run rows).
3. Streamed deltas render into the optimistic tail exactly as today
   (markdown-streaming etc. unchanged).
4. Terminal event with `final_rev` ⇒ `syncTranscript`. Swap rule per
   optimistic entry, in order, against new durable rows with
   seq > (last durable seq at `revAtSend`):
   - user entry ⇔ first new `user` row;
   - tool call/result ⇔ durable row containing the same `tool_call_id`;
   - assistant text/reasoning ⇔ remaining assistant rows of that turn in order.
   Matched optimistic entries are dropped (durable body already fetched for
   the tail segment); unmatched entries **stay** (I6: stop/kill before flush,
   provider error before persistence — same outcome as today's
   "server has no trace ⇒ preserve" rule, but decided by index evidence
   instead of content heuristics).
5. Cross-tab attach (this tab idle, another streams): status poll shows
   `active_response_id` + `transcript_rev`; `syncTranscript` to ≥ `started_rev`,
   then subscribe SSE with replay from event-sequence 0 (existing replay
   buffer in `responseRunManager`); no snapshot-recovery timestamp gate.

Interrupt/stop (`handleSessionInterrupt`) needs no change: whatever was
persisted got rev bumps; whatever wasn't stays optimistic.

## 9. Compaction & session lifecycle (behavior preserved)

- `CompactMessages` keeps old rows and moves `compaction_seq` (sqlite.go:3029);
  index exposes all rows + boundary flags — client scrollback across the
  boundary works as today, including the "don't discard pre-compaction
  display history" rule.
- `ReplaceCompactedMessages`/`ReplaceMessages` appear to clients as a rewrite
  diff (I3) — handled by §7.3 step 2 with no special cases.
- Server-side session runtime eviction (`serve_session.go`) is untouched;
  transcript sync is store-backed and works with no runtime present.

## 10. Single-PR checkpoint order

Internal checkpoints (each compiles, `go test ./...` green; commit per
checkpoint inside the one PR; no feature flag — the final checkpoint is the
only enabled path):

1. **Store:** migration + `bumpTranscriptRev` in all write paths +
   `TranscriptIndexer` on `SQLiteStore` (and `logger.go` store passthrough);
   Go tests for I1/I2/I3 incl. the "every write method bumps rev" sweep test.
2. **API:** `/transcript`, `/transcript/bodies`, status `transcript_rev`,
   `started_rev`/`final_rev` on run lifecycle events; handler tests
   (route dispatch in `handleSessionByID`, caps, ETag/304, coherent-rev race
   test with a concurrent writer goroutine).
3. **Client store:** `transcript-store.js` (skeleton diff, segments, budgets,
   optimistic swap) + `transcript_store_test.js`; wire the Node test into
   `embed_test.go` like `TestMarkdownSetupJS`.
4. **Client integration:** switch `app-sessions.js`/`app-stream.js`/
   `app-render.js` onto the store; delete superseded code (§11); update
   `app_sessions_test.js` / `app_stream_test.js` / `app_render_test.js`.
5. **Wiring:** `index.html` script tag, `sw.js` `SHELL_ASSETS` (+ bump
   `SHELL_CACHE` to `term-llm-shell-v3`), `embed.go` `RenderIndexHTML` /
   `RenderServiceWorker` replacement tables; `gofmt -w .`; full
   `go test ./...` (runs JS suites when node is present).

## 11. Compatibility, deletions, non-goals

**Kept:**
- `GET /v1/sessions/{id}/messages` — still used by non-UI consumers and by
  *stale cached UI* (sw.js is cache-first; after a server deploy a client can
  run the previous JS until the background refetch lands). This is the only
  skew that needs a fallback, and it is served by keeping the old endpoint,
  not by keeping the old client engine. Document this in the handler comment.
- New client fallback for the inverse skew (new cached JS, older server, e.g.
  behind hub proxies): if `/transcript` 404s, walk `/messages?tail=1` and its
  `before_seq` pages to completion, then build skeleton+bodies through a
  converter into the *same* transcript store. Walking every page is required
  to preserve I0 even on the fallback path. One code path, one correctness
  engine; remove the fallback when the hub minimum server version passes this
  release.

**Deleted in this PR (checkpoint 4):**
- Timestamp reconciliation: `showActiveTranscriptCatchUpWarning`,
  `clearActiveTranscriptCatchUpWarning`, `reconcileActiveTranscriptFromStatus`,
  `reconcilePendingActiveTranscriptRefresh`, `recordTranscriptVersionsFromStatus`,
  `numericTranscriptUpdatedAt`, `tailTranscriptUpdatedAt` bookkeeping, and
  their `app_sessions_test.js` cases (replaced by rev-based tests).
- Heuristic merge: `mergeServerMessagesWithLocalState`,
  `localToolGroupCoveredByServerTurn` and helpers,
  `applyTailMessagesToSessionHistory`'s merge half (the conversion half moves
  into the fallback converter), `shouldRecoverActiveResponseFromSnapshot`'s
  `lastSequenceNumber` gate (superseded by `started_rev`).
- localStorage persistence of `session.messages` bodies (one-time migration:
  on load, drop stored bodies, keep metadata).

**Non-goals:** TUI transcript handling, session export/share HTML, hub
dashboard rendering, FTS, side-question overlays, WebRTC, index delta
encoding on the wire, byte-accurate memory accounting.

## 12. Tests

Go (`internal/session/sqlite_test.go`, `cmd/serve_*_test.go`):
- rev bumps for AddMessage (auto+explicit seq), ReplaceMessages,
  ReplaceCompactedMessages, CompactMessages, ClearCompactionBoundary; sweep
  test over all Store write methods (I1).
- concurrent writer vs. `GetTranscriptIndex`/`GetMessagesBySequences`:
  returned rev always describes returned rows (I2; loop N times under
  goroutine writes).
- index contents: system/developer excluded; event/tool included;
  `empty_body` flag matches `sessionMessageEntries` emptiness; compaction flags;
  compact parallel-array shape and no silent index truncation.
- bodies endpoint: ID/range parsing, 512-ID cap → HTTP 400, whole-turn ToolError
  correctness, unknown seq omission.
- run lifecycle: `started_rev` recorded before user-row persistence;
  `final_rev` ≥ rev of last run row (I4); values present in SSE payloads.
- status payload includes `transcript_rev` and retains `transcript_updated_at`.

JS (`transcript_store_test.js`, updated app suites; run via `embed_test.go`):
- diff cases: append, rewrite mid-history, compaction count change, shrink,
  ETag/304 no-op.
- segment math: grouping from roles only (bodies absent), whole-segment
  fetch/eviction, spacer estimates; named regression: bodies loaded for rows
  1–50 and 150–200 yield an explicit single 51–149 gap, never adjacency.
- viewport anchor transaction: while row 22 is visible, append/tail sync,
  interior-gap fill, eviction, and rewrite keep it at the same pixel (±1 px);
  the visible turn is never selected for eviction.
- budget invariants I5 after randomized operation sequences
  (`_checkInvariants`).
- optimistic swap: clean completion, interleaved tool calls, stop-before-flush
  preservation (I6), cross-tab attach at `started_rev`, duplicate SSE replay.
- fallback converter: `/messages` page → identical store state as
  index+bodies for the same fixture.

## 13. Observability

- Store: counters on `session.transcript` (`indexFetches`, `bodyFetches`,
  `rewrites`, `evictions`) exposed as `window.__transcriptStats(sessionId)`
  for debugging and asserted in JS tests; `console.debug` behind the existing
  debug logging toggle.
- Server: `X-Transcript-Rev` response header on both new endpoints (free to
  emit, invaluable in curl/HAR debugging); reuse existing debug logs — no new
  metrics framework.

## 14. Acceptance criteria

1. `go build ./...`, `gofmt -w .` clean, `go test ./...` green (including the
   Node-run JS suites).
2. Two browser tabs on one session: streaming in tab A appears in tab B after
   its next status poll after a lightweight identity-index reconciliation—not
   a full body refetch—and with no catch-up warning UI; killing the server
   mid-stream then reloading preserves the bounded optimistic overlay until the
   index proves persistence.
3. A 5k-message session: activation transfers only the index plus visible
   segment bodies; scrolling loads older segments; materialized turns never
   exceed `maxMaterializedTurns`; localStorage contains no message bodies.
4. Compaction (`/runtime/compact`) preserves visible scrollback exactly as
   before; `/messages` endpoint still serves stale-UI clients.
5. All functions listed in §11 "Deleted" are gone (grep-clean), with their
   behavior covered by rev-based replacements and tests.
6. No feature flag; the rev-based path is the only client sync engine, with
   the single documented `/transcript`-404 fallback.
