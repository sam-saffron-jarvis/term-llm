import type { ProxyState } from '../src/api/proxy-client';
import { button, el, input } from './dom';
import {
  providerApproval,
  providerFamilies,
  providerTitle,
  type ProviderGroup,
} from './provider-groups';

/** Lists stay scannable without a filter until they outgrow one screen. */
export const FILTER_THRESHOLD = 8;

export function providerDot(group: ProviderGroup): HTMLElement {
  const dot = el('span');
  dot.className = `provider-dot provider-dot-${group.status}`;
  dot.setAttribute('role', 'img');
  dot.setAttribute(
    'aria-label',
    `Status: ${group.status}${group.reason ? `, ${group.reason}` : ''}`,
  );
  return dot;
}

/** Clients with any access (the Access list) and those awaiting re-approval. */
export function approvalCounts(group: ProviderGroup, state: ProxyState) {
  let access = 0;
  let partial = 0;
  for (const caller of state.callers) {
    const approval = providerApproval(group, caller);
    if (approval !== 'none') access++;
    if (approval === 'partial') partial++;
  }
  return { access, partial };
}

export function clientCount(count: number): string {
  return `${count} ${count === 1 ? 'client' : 'clients'}`;
}

export function matchesFilter(group: ProviderGroup, value: string): boolean {
  const query = value.trim().toLowerCase();
  if (!query) return true;
  const haystack = [group.id, group.label, ...group.routes.map((route) => route.local_prefix)];
  return haystack.some((value) => value.toLowerCase().includes(query));
}

export interface ProviderListOptions {
  groups: ProviderGroup[];
  state: ProxyState;
  selected?: string;
  filter: string;
  detailID: string;
  /** Returns false when another operation must finish first. */
  onSelect: (id: string) => boolean;
  onFilter: (value: string) => void;
}

/** The master list: one compact row per provider, grouped by family. */
export function providerList(options: ProviderListOptions): HTMLElement {
  const { groups, state, detailID } = options;
  const view = el('nav');
  view.className = 'provider-list';
  view.setAttribute('aria-label', 'Providers');
  const items: { group: ProviderGroup; row: HTMLLIElement }[] = [];
  const sections: { section: HTMLElement; rows: HTMLLIElement[] }[] = [];

  for (const [family, label] of providerFamilies) {
    const entries = groups.filter((group) => group.family === family);
    if (!entries.length) continue;
    const heading = el('h3', label);
    heading.className = 'provider-list-family';
    const list = el('ul');
    list.setAttribute('aria-label', label);
    const rows = entries.map((group) => {
      const row = el('li', providerItem(group));
      items.push({ group, row });
      return row;
    });
    list.append(...rows);
    const section = el('div', heading, list);
    sections.push({ section, rows });
  }

  function providerItem(group: ProviderGroup): HTMLButtonElement {
    const { access, partial } = approvalCounts(group, state);
    const name = el('span', group.id);
    name.className = 'provider-list-name';
    const count = el('span', String(access));
    count.className = 'provider-list-count';
    count.title = `${clientCount(access)} with access`;
    const item = button('', () => {
      if (item.getAttribute('aria-current') === 'true') return;
      options.onSelect(group.id);
    });
    item.className = 'provider-list-item';
    item.dataset.provider = group.id;
    item.dataset.routes = group.routes.map((route) => route.name).join(' ');
    item.setAttribute('aria-controls', detailID);
    if (group.id === options.selected) item.setAttribute('aria-current', 'true');
    const dot = providerDot(group);
    dot.removeAttribute('role');
    dot.removeAttribute('aria-label');
    dot.setAttribute('aria-hidden', 'true');
    item.append(dot, name, count);
    // One secondary line: what is wrong, else who is waiting, else the label.
    const note =
      group.status !== 'green' && group.reason
        ? { text: group.reason, tone: group.status }
        : partial
          ? {
              text: `${partial} ${partial === 1 ? 'client needs' : 'clients need'} approval`,
              tone: 'orange',
            }
          : group.label !== group.id
            ? { text: group.label, tone: '' }
            : undefined;
    if (note) {
      const sub = el('span', note.text);
      sub.className = `provider-list-note${note.tone ? ` provider-list-note-${note.tone}` : ''}`;
      item.append(sub);
    }
    const described = [
      providerTitle(group),
      group.reason,
      `${clientCount(access)} with access`,
      partial ? `${partial} ${partial === 1 ? 'needs' : 'need'} approval` : '',
    ].filter(Boolean);
    item.setAttribute('aria-label', described.join(', '));
    return item;
  }

  const empty = el('p', 'No providers match.');
  empty.className = 'provider-list-empty';
  function applyFilter(value: string) {
    for (const { group, row } of items) row.hidden = !matchesFilter(group, value);
    for (const { section, rows } of sections) section.hidden = rows.every((row) => row.hidden);
    empty.hidden = items.some(({ row }) => !row.hidden);
  }

  if (groups.length >= FILTER_THRESHOLD) {
    const search = input(options.filter, 'search');
    search.className = 'provider-list-filter';
    search.placeholder = 'Filter providers…';
    search.setAttribute('aria-label', 'Filter providers');
    search.addEventListener('input', () => {
      options.onFilter(search.value);
      applyFilter(search.value);
    });
    view.append(search);
  }
  view.append(...sections.map(({ section }) => section), empty);
  applyFilter(groups.length >= FILTER_THRESHOLD ? options.filter : '');
  return view;
}
