import { Fragment, type ComponentChildren } from 'preact';
import { useState } from 'preact/hooks';
import {
  relativeSessionTime,
  runningSessions,
  sessionMessageCountLabel,
  waitingLabel,
} from '../domain/formatting';
import type {
  HubAttentionInboxItem,
  HubInputRequiredItem,
  HubNode,
  HubNodeSession,
} from '../domain/types';
import { inboxItemKey, type HubStore } from '../stores/hub-store';
import { HubRenderBoundary } from './HubRenderBoundary';

type LaneState = 'wait' | 'run' | 'review';

/** Rows a lane shows before "Show more", so one busy lane cannot bury the grid. */
const LANE_ROWS = 5;

/** Joins the present parts of a row's second line with middle dots. */
function joined(parts: ComponentChildren[]) {
  return parts
    .filter((part) => part !== '' && part !== false && part != null)
    .map((part, index) => (
      <Fragment key={index}>
        {index > 0 && ' · '}
        {part}
      </Fragment>
    ));
}

function Row({
  state,
  href,
  title,
  meta,
  stale,
  children,
}: {
  state: LaneState;
  href?: string;
  title: string;
  meta?: ComponentChildren;
  stale?: boolean;
  children?: ComponentChildren;
}) {
  return (
    <div class="now-item">
      <a class={`now-row${stale ? ' is-stale' : ''}`} href={href || '#'} title={title}>
        <span class={`now-glyph ${state}`} aria-hidden="true" />
        <span class="now-title">{title}</span>
        {meta && <span class="now-meta">{meta}</span>}
      </a>
      {children}
    </div>
  );
}

/** One conversation blocked on a question or approval. */
function WaitingRow({ item, stale }: { item: HubInputRequiredItem; stale: boolean }) {
  const old = stale || Boolean(item.stale);
  const label = waitingLabel(
    item.pending_interaction_kinds ?? [],
    Number(item.pending_interaction_count) || 1,
  );
  return (
    <Row
      state="wait"
      href={item.resume_path}
      title={item.title || item.session_id || 'Untitled conversation'}
      stale={old}
      meta={joined([
        item.node_name || item.node_id,
        <span class="now-wait">{label}</span>,
        old ? 'last known state' : relativeSessionTime(item.required_since),
      ])}
    />
  );
}

/** One conversation a node reports as running. */
function RunningRow({
  node,
  session,
  stale,
}: {
  node: HubNode;
  session: HubNodeSession;
  stale: boolean;
}) {
  return (
    <Row
      state="run"
      href={session.resume_path}
      title={session.short_title || session.long_title || session.id}
      stale={stale}
      meta={joined([
        node.name,
        session.active_children && (session.active_run ? 'with subagents' : 'subagents running'),
        sessionMessageCountLabel(session.message_count),
        relativeSessionTime(session.last_message_at),
      ])}
    />
  );
}

/** One finished conversation not visited yet. */
function ReviewRow({
  item,
  stale,
  store,
}: {
  item: HubAttentionInboxItem;
  stale: boolean;
  store: HubStore;
}) {
  const outcome = `${item.outcome || 'completed'}`;
  const title = item.title || item.session_id || 'Untitled conversation';
  const marking = store.markingSeen.value.has(inboxItemKey(item));
  return (
    <Row
      state="review"
      href={item.resume_path}
      title={title}
      stale={stale}
      meta={joined([
        item.node_name || item.node_id,
        <span class={outcome === 'failed' || outcome === 'orphaned' ? 'now-failed' : undefined}>
          {outcome}
        </span>,
        stale ? 'last known state' : relativeSessionTime(item.terminal_at),
      ])}
    >
      {item.store_instance_id && (
        <button
          type="button"
          class="now-seen"
          title="Mark seen"
          aria-label={`Mark ${item.title || item.session_id || 'conversation'} as seen`}
          aria-busy={marking}
          disabled={stale || store.clearingAttention.value || marking}
          onClick={() => void store.markInboxItemSeen(item)}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M3.5 8.5 6.5 11.5 12.5 4.5" />
          </svg>
        </button>
      )}
    </Row>
  );
}

interface LaneRow {
  key: string;
  /** Reconciled row identity: a failed row retries only when fresh data arrives. */
  resetKey: unknown;
  row: ComponentChildren;
}

function Lane({
  state,
  label,
  count,
  action,
  status,
  rows,
}: {
  state: LaneState;
  label: string;
  count: ComponentChildren;
  action?: ComponentChildren;
  status?: ComponentChildren;
  rows: LaneRow[];
}) {
  const [expanded, setExpanded] = useState(false);
  const shown = expanded ? rows : rows.slice(0, LANE_ROWS);
  return (
    <section class="now-lane" aria-label={label}>
      <div class="now-lane-head">
        <span class={`now-glyph ${state}`} aria-hidden="true" />
        <h3>{label}</h3>
        <span class="now-count" aria-live="polite">
          {count}
        </span>
        {action}
      </div>
      {status}
      {rows.length > 0 && (
        <ul class="now-list">
          {shown.map((row) => (
            <li key={row.key}>
              <HubRenderBoundary resetKey={row.resetKey} label="Conversation">
                {row.row}
              </HubRenderBoundary>
            </li>
          ))}
        </ul>
      )}
      {rows.length > LANE_ROWS && (
        <button
          type="button"
          class="now-more"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? 'Show fewer' : `Show ${rows.length - LANE_ROWS} more`}
        </button>
      )}
    </section>
  );
}

/**
 * What needs you, what is running, and what finished unread, across every
 * node. Waiting and review rows come from the Hub's attention projection;
 * running rows come from each node's active sessions. Only lanes with
 * something in them show, from the left, and nothing shows when all is quiet.
 */
export function NowPanel({ store }: { store: HubStore }) {
  const stale = !store.attentionVerified.value || Boolean(store.attentionError.value);
  const nodesStale = !store.nodesVerified.value || Boolean(store.nodeError.value);
  const waiting = store.inputRequired.value;
  const inbox = store.inbox.value;
  const clearing = store.clearingAttention.value;
  const clearStatus = store.attentionClearStatus.value;
  const running = runningSessions(store.nodes.value);
  const listed = new Set(running.sessions.map(({ node }) => node.id));
  const runningRows: LaneRow[] = [
    ...running.sessions.map(({ node, session }) => ({
      key: `${node.id}:${session.id}`,
      resetKey: session,
      row: <RunningRow node={node} session={session} stale={nodesStale} />,
    })),
    // Runs a node counts without listing them, such as ones only its
    // attention snapshot reports, link to the node itself.
    ...running.more.map(({ node, count }) => ({
      key: `${node.id}:more`,
      resetKey: node,
      row: (
        <Row
          state="run"
          href={node.sessions?.resume_path || node.proxy_path}
          title={`${count} ${listed.has(node.id) ? 'more' : 'running'} on ${node.name}`}
          stale={nodesStale}
        />
      ),
    })),
  ];
  const reviewing = inbox.length > 0 || clearing || Boolean(clearStatus);
  if (!waiting.length && !runningRows.length && !reviewing) return null;
  const more = store.attentionHasMore.value;
  const key = (item: { node_id: string; session_id: string }) =>
    `${item.node_id}:${item.session_id}`;
  return (
    <section class="now" aria-label="Now">
      <div class="now-head">
        <h2>Now</h2>
        {(stale || nodesStale) && <span class="hub-cache-marker">last known · updating</span>}
      </div>
      <div class="now-lanes">
        {waiting.length > 0 && (
          <Lane
            key="wait"
            state="wait"
            label="Needs you"
            count={`${Math.max(store.totalInputRequired.value, waiting.length)}${more ? ' · showing first' : ''}`}
            rows={waiting.map((item) => ({
              key: key(item),
              resetKey: item,
              row: <WaitingRow item={item} stale={stale} />,
            }))}
          />
        )}
        {runningRows.length > 0 && (
          <Lane key="run" state="run" label="Running" count={running.total} rows={runningRows} />
        )}
        {reviewing && (
          <Lane
            key="review"
            state="review"
            label="To review"
            count={`${Math.max(store.totalUnseen.value, inbox.length)}${more ? ' · showing newest' : ''}`}
            action={
              <button
                type="button"
                class="hub-btn small ghost"
                disabled={stale || clearing}
                onClick={() => void store.clearAttention()}
              >
                {clearing ? 'Clearing…' : 'Clear all'}
              </button>
            }
            status={
              <div class={clearStatus ? 'hub-error now-status' : 'now-status'} role="status">
                {clearing ? 'Clearing completed review notifications…' : clearStatus}
              </div>
            }
            rows={inbox.map((item) => ({
              key: key(item),
              resetKey: item,
              row: <ReviewRow item={item} stale={stale} store={store} />,
            }))}
          />
        )}
      </div>
    </section>
  );
}
