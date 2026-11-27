import { relativeTime } from '../../domain/time';
import type { HubNode, HubNodeSession } from './types';

export function relativeSessionTime(value: number | string | undefined, now = Date.now()): string {
  const timestamp = typeof value === 'string' ? Date.parse(value) : Number(value);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return '';
  return relativeTime(timestamp, { now });
}

export function countLabel(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function sessionMessageCountLabel(count: number | undefined): string {
  const value = Number(count);
  return Number.isFinite(value) && value > 0 ? countLabel(value, 'message') : '';
}

export function sessionMetaText(
  session: HubNodeSession,
  options: { active?: boolean; unseen?: boolean; now?: number } = {},
): string {
  const parts: string[] = [];
  if (session.interaction_required) {
    const count = Number(session.pending_interaction_count) || 1;
    parts.push(count === 1 ? 'waiting for input' : `${count} decisions waiting`);
  } else if (options.active || session.active_run || session.active_children) {
    parts.push('active now');
  } else if (options.unseen) {
    parts.push('to review');
  }
  const relative = relativeSessionTime(session.last_message_at, options.now);
  if (relative) parts.push(relative);
  const messages = sessionMessageCountLabel(session.message_count);
  if (messages) parts.push(messages);
  return parts.join(' · ');
}

const APPROVALS: Record<string, string> = {
  'approval.shell': 'Shell approval',
  'approval.workspace': 'Workspace access',
  'approval.file_write': 'File write approval',
};

/** Names what a blocked conversation waits on from its broad interaction kinds. */
export function waitingLabel(kinds: string[] = [], count = 1): string {
  if (count > 1) return `${count} decisions`;
  const approval = kinds.find((kind) => kind.startsWith('approval'));
  if (!approval) return 'Question';
  if (kinds.includes('ask_user')) return 'Question and approval';
  return APPROVALS[approval] ?? 'Approval';
}

export interface RunningSessions {
  /** Listed running conversations, in node order; waiting ones belong to Needs you. */
  sessions: { node: HubNode; session: HubNodeSession }[];
  /** Running conversations a node counted but did not list. */
  more: { node: HubNode; count: number }[];
  total: number;
}

/**
 * Running conversations across every node. Nodes list only their first few
 * active sessions, so each node's count also reports the ones left unlisted.
 */
export function runningSessions(nodes: HubNode[]): RunningSessions {
  const result: RunningSessions = { sessions: [], more: [], total: 0 };
  for (const node of nodes) {
    const sessions = node.sessions;
    if (!sessions) continue;
    const active = Array.isArray(sessions.active) ? sessions.active : [];
    const explicit = sessions.active_count;
    const count =
      typeof explicit === 'number' && Number.isFinite(explicit) && explicit > active.length
        ? explicit
        : active.length;
    let waiting = 0;
    for (const session of active) {
      if (session?.interaction_required) waiting++;
      else if (session?.id) result.sessions.push({ node, session });
    }
    if (count > active.length) result.more.push({ node, count: count - active.length });
    result.total += count - waiting;
  }
  return result;
}

export function nodeResumePath(node: HubNode): string {
  const sessions = node.sessions;
  return (
    sessions?.resume_path ||
    sessions?.active?.[0]?.resume_path ||
    sessions?.recent?.[0]?.resume_path ||
    ''
  );
}

export function shellQuote(value: string): string {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

export function currentHubURL(location: Pick<Location, 'origin'>, basePath: string): string {
  return `${location.origin}${basePath || ''}/`;
}

export function buildRegistrationCommand(hubURL: string): string {
  return `export HUB_URL=${shellQuote(hubURL)}
export HUB_REGISTRATION_TOKEN="<copy registration token from above>"
export NODE_TOKEN="$(openssl rand -hex 32)"
export NODE_ID="docker-$(hostname)"

# Add --agent, --port, or --base-path if this node needs them.
term-llm serve web \\
  --token "$NODE_TOKEN" \\
  --hub-url "$HUB_URL" \\
  --hub-node-id "$NODE_ID" \\
  --hub-node-name "Docker $(hostname)" \\
  --hub-connect reverse \\
  --hub-register \\
  --hub-registration-token "$HUB_REGISTRATION_TOKEN"`;
}

export function activeSessionCount(nodes: HubNode[]): number {
  return nodes.reduce((total, node) => {
    if (!node.sessions) return total;
    const explicit = node.sessions.active_count;
    if (typeof explicit === 'number' && Number.isFinite(explicit) && explicit > 0)
      return total + explicit;
    return total + (Array.isArray(node.sessions.active) ? node.sessions.active.length : 0);
  }, 0);
}
