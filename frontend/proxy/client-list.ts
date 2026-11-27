import type { ProxyCaller, ProxyClient } from '../src/api/proxy-client';
import { providerName, providerTitle, type ProviderGroup } from './provider-groups';
import { actions, confirmAction, dateTime, disclosure, el, type UIContext } from './dom';

const byProviderName = new Intl.Collator(undefined, { sensitivity: 'accent', numeric: true });

const DAY = 24 * 60 * 60 * 1000;

/** Compact, scannable timestamp: "Today 2:28 PM", "Yesterday 9:40 AM", "Nov 16". */
export function shortDate(
  value: string | number | undefined,
  empty = 'Never',
  now = new Date(),
): string {
  if (!value) return empty;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Unknown';
  const time = date.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  if (date.getTime() >= startOfToday && date.getTime() < startOfToday + DAY) return `Today ${time}`;
  if (date.getTime() >= startOfToday - DAY && date.getTime() < startOfToday)
    return `Yesterday ${time}`;
  return date.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    ...(date.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }),
  });
}

/** Relative activity for recent use ("5 min ago"), falling back to shortDate. */
export function lastSeen(value: string | number | undefined, now = new Date()): string {
  if (!value) return 'never';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'unknown';
  const minutes = Math.floor((now.getTime() - date.getTime()) / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} hr ago`;
  const days = Math.floor(hours / 24);
  if (days < 2) return 'yesterday';
  if (days < 7) return `${days} days ago`;
  return shortDate(value, 'never', now);
}

/**
 * Short ID suffixes for clients that share a name, so identical names stay
 * distinguishable. Derived from the public client ID, never the token.
 */
export function duplicateSuffixes(callers: ProxyCaller[]): Map<string, string> {
  const groups = new Map<string, ProxyCaller[]>();
  for (const caller of callers)
    groups.set(caller.name, [...(groups.get(caller.name) ?? []), caller]);
  const out = new Map<string, string>();
  for (const group of groups.values()) {
    if (group.length < 2) continue;
    const ids = group.map((caller) => caller.id.replace(/^c_/, ''));
    let length = 4;
    while (
      length < Math.max(...ids.map((id) => id.length)) &&
      new Set(ids.map((id) => id.slice(0, length))).size < ids.length
    )
      length++;
    group.forEach((caller, i) => out.set(caller.id, ids[i].slice(0, length)));
  }
  return out;
}

/** Most recently used first; never-used clients by newest creation. */
export function sortCallers(callers: ProxyCaller[]): ProxyCaller[] {
  const created = (caller: ProxyCaller) => new Date(caller.created).getTime() || 0;
  return [...callers].sort(
    (a, b) => (b.last_used ?? 0) - (a.last_used ?? 0) || created(b) - created(a),
  );
}

export function clientLabel(caller: ProxyCaller, suffixes: Map<string, string>): string {
  const suffix = suffixes.get(caller.id);
  return suffix ? `${caller.name} #${suffix}` : caller.name;
}

function nameNode(caller: ProxyCaller, suffixes: Map<string, string>): Node[] {
  const suffix = suffixes.get(caller.id);
  if (!suffix) return [document.createTextNode(caller.name)];
  const id = el('span', `#${suffix}`);
  id.className = 'client-id';
  return [document.createTextNode(`${caller.name} `), id];
}

interface ProviderLabel {
  name: string;
  note?: string;
  caution?: boolean;
}

// Revoked rows summarize their former access, including legacy partial grants.
function approvalLabels(caller: ProxyCaller, groups: ProviderGroup[]): ProviderLabel[] {
  const labels: ProviderLabel[] = [];
  const granted = new Set(caller.grants);
  const grouped = new Set<string>();
  for (const group of groups) {
    let covered = 0;
    for (const route of group.routes) {
      grouped.add(route.name);
      if (granted.has(route.name)) covered++;
    }
    if (!covered) continue;
    const partial = covered < group.routes.length;
    labels.push({
      name: providerName(group),
      note: partial ? (caller.revoked ? ' (partial)' : ' (needs approval)') : '',
      caution: partial && !caller.revoked,
    });
  }
  // Ungrouped grants still represent access and belong in the same sorted list.
  for (const name of granted) if (!grouped.has(name)) labels.push({ name });
  return labels.sort((a, b) => byProviderName.compare(a.name, b.name));
}

function providerSummary(labels: ProviderLabel[]): HTMLElement {
  const names = labels.map(({ name, note, caution }) => {
    const span = el('span', name + (note ?? ''));
    if (caution) span.className = 'client-grant-caution';
    return span;
  });
  const line = el('span');
  line.className = 'client-provider-line';
  const cell = el('div', line);
  cell.className = 'client-grants';
  if (!names.length) {
    line.textContent = 'No providers';
    return cell;
  }
  const full = names.map((n) => n.textContent).join(', ');
  cell.setAttribute('role', 'group');
  cell.setAttribute('aria-label', `Providers: ${full}`);
  line.setAttribute('aria-hidden', 'true');
  cell.title = full;
  function show(count: number) {
    const parts: (Node | string)[] = [];
    for (const span of names.slice(0, count)) {
      if (parts.length) parts.push(', ');
      parts.push(span);
    }
    if (count < names.length) {
      const more = el(
        'span',
        count
          ? ` and ${names.length - count} more`
          : `${names.length} provider${names.length === 1 ? '' : 's'}`,
      );
      more.className = 'client-grant-more';
      parts.push(more);
    }
    line.replaceChildren(...parts);
  }
  show(names.length);
  if (typeof ResizeObserver !== 'undefined') {
    const observer = new ResizeObserver(([entry]) => {
      if (!cell.isConnected) return observer.disconnect();
      const width = entry.contentRect.width;
      if (!width) return; // A closed revoked-clients disclosure has no layout yet.
      show(names.length);
      if (line.getBoundingClientRect().width <= width) return;
      // Reserve room for the actual suffix, not an estimated character count.
      let lo = 0,
        hi = names.length - 1;
      while (lo < hi) {
        const mid = Math.ceil((lo + hi) / 2);
        show(mid);
        if (line.getBoundingClientRect().width <= width) lo = mid;
        else hi = mid - 1;
      }
      show(lo);
    });
    observer.observe(cell);
  }
  return cell;
}

function lastUsed(caller: ProxyCaller, now: Date): HTMLElement {
  const label = el('span', 'Last used ');
  label.className = 'visually-hidden';
  const node = el('span', label, shortDate(caller.last_used, 'Never', now));
  node.className = 'client-last';
  if (!caller.last_used) node.classList.add('never');
  else {
    node.title = dateTime(caller.last_used);
    if (!caller.revoked && now.getTime() - caller.last_used < DAY) node.classList.add('recent');
  }
  return node;
}

export function clientListHeader(): HTMLElement {
  const head = el('div', el('span', 'Client'), el('span', 'Providers'), el('span', 'Last used'));
  head.className = 'client-list-head';
  head.setAttribute('aria-hidden', 'true');
  return head;
}

// A client summary; active rows toggle a detail panel, revoked rows can be deleted.
export function clientRow(
  caller: ProxyCaller,
  suffixes: Map<string, string>,
  keyNames: ReadonlyMap<string, string>,
  groups: ProviderGroup[],
  options: { panel: HTMLElement; toggle: (open: boolean) => void } | { action: HTMLElement },
  now = new Date(),
): HTMLElement {
  const heading = el('h2');
  heading.tabIndex = -1;
  heading.className = 'client-name';
  const metadata = [];
  if (caller.created_by) {
    const keyName = keyNames.get(caller.created_by);
    metadata.push(keyName ? `Created by API key ‹${keyName}›` : 'Created by a revoked API key');
  }
  if (caller.external_id) metadata.push(`External ID: ${caller.external_id}`);
  const detail = 'panel' in options ? options : undefined;
  const metadataNodes: HTMLElement[] = metadata.map((text) => el('p', el('small', text)));
  if ('action' in options) metadataNodes.push(options.action);
  const identity = metadataNodes.length ? el('div', heading, ...metadataNodes) : heading;
  const main = el(
    'div',
    identity,
    providerSummary(approvalLabels(caller, groups)),
    lastUsed(caller, now),
  );
  main.className = 'client-row-main';
  const row = el('article', main);
  row.className = 'client-row';
  row.dataset.focus = `caller:${caller.id}`;
  if (!detail) {
    heading.append(...nameNode(caller, suffixes));
    return row;
  }
  const toggle = el('button', ...nameNode(caller, suffixes));
  toggle.type = 'button';
  toggle.className = 'client-toggle';
  toggle.setAttribute('aria-expanded', 'false');
  detail.panel.id ||= `client-detail-${caller.id}`;
  detail.panel.hidden = true;
  toggle.setAttribute('aria-controls', detail.panel.id);
  toggle.addEventListener('click', () => detail.toggle(!row.classList.contains('expanded')));
  heading.append(toggle);
  const chevron = el('span');
  chevron.className = 'client-chevron';
  chevron.setAttribute('aria-hidden', 'true');
  main.append(chevron);
  row.append(detail.panel);
  return row;
}

export function revokedClients(
  callers: ProxyCaller[],
  suffixes: Map<string, string>,
  keyNames: ReadonlyMap<string, string>,
  groups: ProviderGroup[],
  api: ProxyClient,
  context: UIContext,
): HTMLDetailsElement {
  const focusKey = 'revoked-clients';
  const historyNote = 'Request history is kept.';
  const deleteAll = confirmAction(
    'Delete all revoked',
    async () => {
      await api.purgeAllRevokedCallers();
      await context.refresh();
      context.announce(`Deleted all revoked clients. ${historyNote}`);
    },
    context,
    `Permanently delete all revoked clients? ${historyNote}`,
  );
  const rows = callers.map((caller) => {
    const label = clientLabel(caller, suffixes);
    const remove = confirmAction(
      'Delete…',
      async () => {
        await api.purgeCaller(caller.id);
        await context.refresh(focusKey);
        context.announce(`Deleted ${label}. ${historyNote}`);
      },
      context,
      `Permanently delete ${label}? ${historyNote}`,
    );
    remove.setAttribute('aria-label', `Delete ${label}`);
    return clientRow(caller, suffixes, keyNames, groups, { action: actions(remove) });
  });
  const list = el('div', ...rows);
  list.className = 'client-list revoked-list';
  const group = disclosure(`Revoked (${callers.length})`, actions(deleteAll), list);
  group.className = 'client-revoked';
  const summary = group.querySelector('summary');
  if (summary) {
    summary.dataset.focus = focusKey;
    summary.tabIndex = 0;
  }
  return group;
}

export function setRowExpanded(row: HTMLElement, open: boolean): void {
  const panel = row.querySelector<HTMLElement>('.client-detail');
  if (panel) panel.hidden = !open;
  row.classList.toggle('expanded', open);
  row.querySelector('.client-toggle')?.setAttribute('aria-expanded', String(open));
}

export type ProviderApprovalState = 'none' | 'partial' | 'approved';

export interface ProviderApprovalChange {
  id: string;
  approved: boolean;
}

export interface ProviderChips {
  fields: HTMLFieldSetElement;
  /** Provider IDs that are ticked and can be approved. */
  selected: () => string[];
  /** Explicit changes only; untouched legacy partial approvals are omitted. */
  changes: () => ProviderApprovalChange[];
  reset: () => void;
}

interface ApprovalChoice {
  group: ProviderGroup;
  initial: ProviderApprovalState;
  blocked: readonly string[];
  control: HTMLInputElement;
  label: HTMLLabelElement;
  text: HTMLSpanElement;
  touched: boolean;
}

function paintApprovalChoice(choice: ApprovalChoice): void {
  const unavailable = choice.initial === 'none' && choice.blocked.length > 0;
  const suffix = unavailable
    ? ' (shared subscription not ready)'
    : choice.control.indeterminate
      ? ' (needs approval)'
      : '';
  choice.text.textContent = providerName(choice.group) + suffix;
  const routes = choice.group.routes.map((route) => route.name).join(', ');
  const title = providerTitle(choice.group);
  choice.label.title = unavailable
    ? `Enable and connect a shared subscription for ${choice.blocked.join(', ')} before approving ${title}. Routes: ${routes}`
    : `${title}. Routes: ${routes}`;
}

/**
 * Provider-wide approval checkboxes. A legacy partial approval starts
 * indeterminate and is preserved unless the operator touches it; an explicit
 * uncheck revokes every current route in that provider.
 */
export function providerChips(
  groups: ProviderGroup[],
  approvals: ReadonlyMap<string, ProviderApprovalState>,
  legend: string,
  unavailable: ReadonlyMap<string, readonly string[]> = new Map(),
): ProviderChips {
  const choices: ApprovalChoice[] = groups.map((group) => {
    const initial = approvals.get(group.id) ?? 'none';
    const blocked = unavailable.get(group.id) ?? [];
    const control = el('input');
    control.type = 'checkbox';
    control.checked = initial === 'approved';
    control.indeterminate = initial === 'partial';
    control.disabled = blocked.length > 0 && initial === 'none';
    control.dataset.provider = group.id;
    const text = el('span');
    const label = el('label', control, text);
    label.className = 'check grant-chip';
    const choice: ApprovalChoice = {
      group,
      initial,
      blocked,
      control,
      label,
      text,
      touched: false,
    };
    paintApprovalChoice(choice);
    control.addEventListener('change', () => {
      choice.touched = true;
      control.indeterminate = false;
      paintApprovalChoice(choice);
    });
    return choice;
  });
  const chips = el('div', ...choices.map((choice) => choice.label));
  chips.className = 'grant-chips';
  const fields = el('fieldset', el('legend', legend), chips);
  fields.className = 'client-grant-fields';
  return {
    fields,
    selected: () =>
      choices
        // The submit guard temporarily disables every control before invoking us.
        .filter(
          (choice) =>
            !(choice.initial === 'none' && choice.blocked.length) && choice.control.checked,
        )
        .map((choice) => choice.group.id),
    changes: () => {
      const out: ProviderApprovalChange[] = [];
      for (const choice of choices) {
        // Existing approvals can be revoked, but unavailable routes cannot gain access.
        if (choice.blocked.length && choice.control.checked) continue;
        if (choice.initial === 'partial') {
          if (choice.touched) out.push({ id: choice.group.id, approved: choice.control.checked });
          continue;
        }
        if (choice.control.checked !== (choice.initial === 'approved'))
          out.push({ id: choice.group.id, approved: choice.control.checked });
      }
      return out;
    },
    reset: () =>
      choices.forEach((choice) => {
        choice.touched = false;
        choice.control.checked = choice.initial === 'approved';
        choice.control.indeterminate = choice.initial === 'partial';
        paintApprovalChoice(choice);
      }),
  };
}
