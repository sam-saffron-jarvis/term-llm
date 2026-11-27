# Web fork stepper and visible path notes

Status: implemented

Sam picked concept A ("Stepper") from three rendered mocks on 2026-11-27. This plan
merges two independent planning passes (astra and fable) and resolves the points where
they disagreed. Line numbers are approximate and as of the working tree on 2026-11-27.

Reference mock (gitignored, local only): `tmp/forkmocks/proto.html` (`runA`, `notesCard`
in `tmp/forkmocks/proto.js`), screenshots `tmp/forkmocks/shots/A-*.png`, today's behaviour
`tmp/forkmocks/shots/cur-2-busy.png` and `cur-3-forked.png`. Full brief:
`tmp/forkmocks/BRIEF.md`.

## Problem

1. Forking with notes in the web UI blocks the dialog for 5–120 s. The dialog only greys
   its buttons and shows the plain text "Creating path…", with no spinner or progress.
2. After the fork, the note renders as one faint, collapsed, italic `<details>` row
   (`frontend/src/components/Transcript.tsx:1290`). Users miss it and cannot tell whether
   the notes worked. Forks and their source also look identical in the sidebar.

## Outcome

- After the user picks **Bring concise notes** or **Focused context**, the dialog stays
  open and becomes a stepper:
  1. Create path #N
  2. Read K later messages in #S
  3. Write path notes, with a draft preview
  4. Open path
- The steps show real progress. "Skip notes" and "Open now" are always available.
- The new path shows:
  - a "Forked from #S" divider;
  - a prominent path-notes card, expanded while it is the latest thing in the path;
  - a "Fork of #S" header badge;
  - a fork marker on its sidebar row.
- A clean branch keeps today's quick flow. It gains the divider, the badge and the sidebar
  marker.

## Decisions

### Transport: keep the blocking POST, and add status GET and cancel DELETE

`POST /v1/sessions/:child/path-notes` stays synchronous JSON for existing callers. Add two
methods on the same route (`serve_session_routes.go:368`):

- `GET` returns a status snapshot.
- `DELETE` cancels.

The dialog polls `GET` about every 700 ms while it tracks work.

Rejected alternatives:

- **SSE/NDJSON POST.** The work context is detached (`detachedBranchWorkContext`,
  `serve_responses_durable.go:32`), so a stream gives no cancellation. Reload and "open
  now" would still need a status read.
- **Event bus progress.** The bus is a payload-free invalidation channel. It is still used
  for completion: `serveEventSessionTranscriptChanged`, reason `branch_path_note`, is
  published after `AddMessage`, and the coordinator reloads the active transcript.

**Job record.** `s.branchNotes` (`serve_server.go:77`) changes its value from `struct{}{}`
to `*branchNoteJob`, which holds:

- `mu` and `state`: `reading`, `writing`, `saving`, or `skipped`;
- `mode` and `focus`;
- `sourceSessionID`, `sourceMessages`, `omittedMessages`, `readFiles`, `modifiedFiles`;
- `model`, `startedAt`, `writingAt`;
- `preview`, capped at about 4 KB;
- `cancel`.

The job is deleted on completion, as today. Map membership keeps its current meaning
("notes are being prepared; refuse sends", `serve_notification_origin.go:170`). Terminal
outcomes reach the client through the POST result or, after a reload, through the presence
or absence of the durable note.

**Status wire type.** `sessionPathNotesStatus`:

- `state`: `idle`, `reading`, `writing`, or `saving`;
- `mode`, `focus`, `source_session_id`;
- `source_messages`, `omitted_messages`;
- `read_files`, `modified_files`;
- `model`, `started_at`, `writing_at`;
- `preview`.

Register the type for TS generation (`internal/tsgen`, `go run ./cmd/gentsapi` / `make
api-types`).

**Engine hooks.** In `internal/llm/engine/path_notes.go`, add two optional hooks to
`PathNotesConfig`:

- `OnPrepared func(PathNotesPrepared)` is called just before the provider stream starts.
  Counts and read/modified files are already computed by then (around lines 72–76 and
  119–128).
- `OnText func(delta string)` is forwarded from the existing text-stream observer around
  line 154.

`generateBranchPathNote` (`serve_responses_durable.go`) takes the hooks and updates the
job.

**Shared flights.** `branchPathNoteFlights` shares one helper result across requests with
the same source and anchor. Cancelling one child's job must not cancel a flight another
waiter still uses. Either detach the waiter, or cancel the flight only when no other waiter
remains. Pick the simpler correct option and test it.

### What each step shows (honest data only)

| Step | Data | Becomes done when |
|---|---|---|
| Create path #N | `session.number` from the branch response; client-timed | `POST /branches` returns. Do not wait for the sidebar refresh before starting notes. |
| Read K later messages in #S | `source_messages` (+ "M omitted" if non-zero), modified/read file counts | the status reaches `writing` |
| Write path notes | `model` (the source session's model, not a "mini" model), elapsed seconds, draft preview | the POST resolves with a saved note |
| Open path | client-timed | `selectSession(child)` resolves |

- Drop the mock's "up to 30s". The real cap is 2 minutes (`branchPathNoteWorkTimeout`).
- The preview is plain pre-wrap text labelled **Draft**. It is shown before the tool-use
  guard and word truncation, so it is never rendered as the final note.
- While the preview is empty, show a shimmer skeleton. Reasoning-summary-only models never
  produce one.
- Step 2 may flash past. That is honest.
- Per-step durations:
  - steps 2 and 3 use server timestamps: `started_at` → `writing_at`, then `writing_at` →
    POST resolution;
  - steps 1 and 4 use the client's monotonic clock.
- Elapsed seconds tick visually but are `aria-hidden`. A single polite live region
  announces step transitions only.

### Cancellation and exits

**Skip notes** sends `DELETE`. Under `job.mu`:

- If the state is not yet `saving`, it becomes `skipped` and the context is cancelled.
- The save path takes the same lock, re-checks the state, sets `saving`, and only then
  calls `AddMessage` and publishes.

A late provider result therefore can never insert a note. `DELETE` returns `{cancelled,
note_saved}`:

- If the save won, the client opens the path with the notes and shows an info toast. It
  never claims the path is clean.
- The cancelled POST returns an error that the client treats as an expected skip, not an
  error toast.

**Open now** closes the dialog and selects the child once. The POST promise and polling
continue, owned by `BranchStore` rather than the modal.

- The child transcript shows a `PathNotesPending` card at the end: spinner, step chips,
  draft preview, and a "Skip notes" link.
- The composer's send is disabled with the hint "Waiting for path notes…". The server
  already refuses sends with 409. There is no TUI-style queue in v1, and the draft text is
  kept.
- When the POST settles, or a transcript-changed event arrives, reconcile the active
  transcript once. The durable note replaces the pending card; never synthesize a note
  message client-side.
- On failure, show a toast. The pending card becomes "Notes couldn't be prepared · Retry ·
  Dismiss" and send unlocks.
- Background completion must never change the selected session or dismiss a newer modal.

**Failure inside the dialog.** Step 3 is marked failed and the dialog offers **Retry
notes** and **Open without notes**:

- Retry re-POSTs for the same child and never re-branches.
- A 409 "already active" response means attach and poll.

**Close/Escape while working.** The close button is hidden, and Escape and backdrop clicks
are inert (`dismissDisabled`). The dialog keeps focus containment, and focus moves to "Open
now". Before submission, behaviour is unchanged.

**Reload mid-generation.**

- The work continues server-side.
- `webBranchTreeResponse` gains an optional `path_notes` status snapshot, omitted when
  idle.
- The tree is fetched on select. Verify this in `selection-store.ts`, and add the fetch if
  it is missing.
- An active snapshot resumes the pending card and polling. The dialog never reopens, and
  nothing is re-created at startup.
- A failure that happened during the reload is not replayed.

**Late placement guard.** `AddMessage(..., -1)` appends. A POST for a child that already
has rows beyond its copied prefix (user turns after the fork) returns 409, so a note can
never land after new turns.

**Idempotency.** `BranchStore.retryOperation` (source+anchor UUID, cleared on a confirmed
child ID) is unchanged. Every later notes call addresses the confirmed child. The duplicate
click guard, the modal-target guard, prefill and clean-command auto-send are preserved.

### Post-fork UI

**Path notes card** replaces the `<details>` at `Transcript.tsx:1290`. It uses the existing
sanitized `Markdown`.

- Classes: `.path-notes-card`, `-head`, `-body`, `-chips`, `-foot`.
- Title: "Path notes". Subtitle: "from #S · not authoritative". Do not count bullets.
- Expanded state is stateless: the card is expanded while no user or assistant message
  follows it in the transcript, and collapsed by default once the path continues. A manual
  toggle is component-local.

Chips come from `CorePathNoteProvenance`:

| Field | Chip |
|---|---|
| `source_messages` | "K later messages read" |
| `omitted_messages` | "M omitted", only when non-zero |
| `modified_files` | "N files modified", with the paths in `title` |
| `read_files` | "N files read", with the paths in `title` |
| `focus` | truncated focus |
| `model` | model |

There is no token chip and no schema change; usage is accounted to the source session.

  - **Open #S** calls `resolveAndSelectSession`.
  - **Inspect** opens the existing inspect modal.
- Number and title for #S come from the active tree's parent node. Fall back to "an earlier
  path".

**Fork divider** (`.fork-divider`, "Forked from #S title"). Its source is the active tree
node's `parent_session_id`, `fork_after_message_id` and `copied_anchor_message_id`.

- Place it immediately before a path note whose provenance matches this edge. Otherwise
  place it after the group that contains `durableRowId === copied_anchor_message_id`.
- Use the **copied** anchor, not the parent anchor.
- If the anchor is 0, place it at the top.
- If the anchor is paged out or compacted away, do not render it.
- Suffix: "· K later messages there" only when matching provenance supplies
  `source_messages`; "· clean, nothing carried" for clean forks.

**Header badge.** `.header-title-badge.fork-of-badge` reads "Fork of #S", sits next to
`branch-tree-trigger` in `HeaderBreadcrumb.tsx`, and opens the source when clicked.

**Sidebar marker.**

- Today's wire carries no fork data: forks store NULL `parent_id` (`branch.go:~196`), and
  `parent_session_id` on web entries means delegation.
- Add optional `branch_parent_session_id` and `branch_parent_number` to web session entries
  for every list projection the sidebar uses (recent, grouped, paginated, search, selected
  session), sourced from `session_branches`.
- Do not add a per-row tree request or an N+1 query. Do not change the delegated-parent
  semantics.
- The row shows the `fork` icon before the title and "Fork of #S · <date>" as meta,
  replacing the message count, as in the mock.
- Bump the client cache schema if cached session shapes change (`stores/workspace-cache.ts`).

## File plan (guidance; follow ratchets: new code in new files, oversize files may not grow)

Backend:

- New `internal/app/serve/serve_branch_notes.go`:
  - the job type and the status, GET and DELETE handlers;
  - the body of `handleSessionPathNotes`, moved out of `serve_branch.go` and split into
    small functions.
- Edits to `serve_branch.go` (tree `path_notes`), `serve_server.go`,
  `serve_session_routes.go`, `serve_responses_durable.go`, `engine/path_notes.go`.
- Session list/get plumbing for the branch-parent fields: `internal/session` types and
  queries, and the web entry mapping. Put new query code in a new file where needed.
- Regenerate `frontend/src/api/generated/types.ts`.

Frontend:

- New files:
  - `components/BranchStepper.tsx`;
  - `components/PathNotes.tsx` (`PathNoteCard`, `PathNotesPending`, `ForkDivider`);
  - `stores/branch-progress.ts` (step model, poller, pending notes per child).
- Edits:
  - `BranchModals.tsx`, `branch-store.ts`, `api/endpoints.ts`;
  - `Transcript.tsx` (delegate; net shrink);
  - `domain/{types,transcript}.ts`;
  - `HeaderBreadcrumb.tsx`, `Sidebar.tsx`, `Composer.tsx`;
  - `styles/features/branching.css`: stepper; a stacked-actions layout at ≤480 px; static
    indicators under `prefers-reduced-motion`.

Docs: `docs-site/content/reference/sessions.md` (~line 50): web stepper, skip, open now,
card.

## Tests

Go, using a gated mock provider and no network:

- Status reports `reading` then `writing` with counts and a preview while the provider is
  blocked.
- DELETE mid-generation: no note row, the job is removed, a later POST succeeds.
- DELETE after save: `note_saved: true`.
- The cancel/save race is decided under the lock.
- Shared-flight cancel does not break another waiter.
- The tree includes `path_notes` only while a job is active.
- GET and DELETE routing and auth.
- Late-placement 409.
- Branch-parent fields on all list projections, with delegated rows unaffected.
- Engine: `OnPrepared` fires before streaming, and `OnText` receives the deltas.

Frontend store (`app-store.test.ts`):

- Skip calls cancel and opens the path clean.
- Skip losing the race opens the path with notes.
- Open now selects before the notes resolve and sets pending state.
- Retry reuses the child.
- 409 attaches.
- The existing idempotency tests stay green.
- Background completion does not change the selection.

Frontend components:

- Stepper states, the live region, and the hidden close button.
- Card expanded when it is last and collapsed when followed.
- Chip mapping.
- Divider placement in each case.
- Header badge, sidebar marker, and disabled send while pending.

E2E (optional but preferred): one mocked-API Playwright spec covering fork with notes →
stepper visible → card visible, on a narrow viewport.

## Staging

Each stage lands independently:

1. Post-fork visibility: card, divider and header badge (frontend only).
2. Sidebar wire fields and marker.
3. Backend job, status/cancel endpoints, engine hooks and tree snapshot.
4. Stepper dialog with skip, open now and retry; pending card; send gate.
5. Docs, e2e, and the full checks (`make check-changed ARGS=--all`, `make complexity`).

## Implementation notes

- The selection store already fetches the tree asynchronously on select. Its existing
  refresh adopts the active `path_notes` snapshot; it does not add a startup request.
- The shared flight belongs to `/v1/responses` and is keyed by source and idempotency key.
  The web POST owns generation per child. A gated test verifies cancellation isolation
  between these independent workflows; it does not exercise shared-flight waiter accounting.
- POST keeps its blocking JSON contract and adds `note_saved` and `cancelled`. A skip
  returns successful cancelled JSON instead of a retryable HTTP 408; a deadline is a
  504. DELETE records a two-minute, per-child in-memory skip marker, atomically with
  job admission. The client sends it as soon as the child is known, even before a
  status poll or when POST was never started. A save that won the lock is not cancelled;
  DELETE waits at most 250 ms for it, then returns the current `saving` snapshot.
  The SQLite append rechecks placement in its write transaction. Provider attempt
  discards clear the preview, and observed usage is still recorded on cancellation.
- The completion event carries the committed transcript revision. The client rejects
  older selected-session bodies, coalesces refreshes through the highest requested
  revision, matches both source and anchor, and keeps the send gate until a reported
  save is visible. If event-driven reload fails, the coordinator can retry. Reading
  duration uses server timestamps; writing time is measured from the first client
  observation to client completion, so it is approximate to the polling interval.
- Stateful jobs and branch context helpers live in `serve/branchnotes`; `serve/wire`
  only owns wire payloads. The generic SQLite append lives in `sqlite_message_append.go`.
  Helper generation stays in `app/runtime` and tree ordering in `session/branchtree`.
- Fork views, styles, notes polling, writes, and note reconciliation load on demand.
  Eager code keeps branch discovery, transcript/sidebar integration and the send gate.
  The measured entry is 548,874 B raw / 163,297 B via Go gzip level 6. This is
  4,324 B / 1,647 B above the pre-fork caps (544,550 B / 161,650 B), down from the
  first implementation's ~553.97/164.73 kB. The entry cap is 549,050 B / 163,450 B;
  the lazy progress chunk has its own guard. No Go ratchet or historical payload
  baseline was changed.
- Found after the fact (2026-11-27): the SQLite transcript index and body reads
  filtered every `developer` row in SQL, so saved path notes never reached the web
  client and every fork rendered as "clean". They now drop only non-note developer
  rows (`Message.hiddenFromTranscript`), including in the fallback indexer.
  `truncateWords` also no longer collapses the notes' Markdown onto one line.
- Also found after the fact: CLI providers (claude-bin) persist a turn as the
  assistant text row followed by its tool rows, so forking "after" a response
  copied the text but dropped its tool results, leaving the media it embedded
  "unavailable" and counting those results as later context. `CreateBranch`
  and `MessagesAfterBranchAnchor` now extend the copy boundary over the tool rows
  directly after the anchor (`branchtree.TurnEnd`/`TurnEndSQL`);
  `fork_after_message_id` still names the chosen response.
