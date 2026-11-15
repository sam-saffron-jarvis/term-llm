import type {
  ProxyClient,
  ProxyRequest,
  ProxyState,
  RequestFilters,
} from '../src/api/proxy-client';
import {
  actions,
  button,
  card,
  dateTime,
  disclosure,
  download,
  el,
  field,
  input,
  select,
  type UIContext,
} from './dom';

const outcomes: [string, string][] = [
  ['complete', 'Complete'],
  ['http_error', 'Upstream HTTP error'],
  ['transport_error', 'Connection error · result uncertain'],
  ['canceled_or_timeout', 'Canceled / timed out · result uncertain'],
  ['incomplete', 'Incomplete stream · result uncertain'],
  ['active', 'In progress'],
  ['unknown', 'Unknown · result uncertain'],
];
export function outcomeLabel(value: string): string {
  return outcomes.find(([key]) => key === value)?.[1] ?? 'Unknown · result uncertain';
}
export function localPath(request: Pick<ProxyRequest, 'local_prefix' | 'path'>): string {
  return (request.local_prefix ?? '').replace(/\/+$/, '') + '/' + request.path.replace(/^\/+/, '');
}
function unit(value: number, suffix: string): string {
  if (suffix === 'ms' && Number(value ?? 0) === 0) return '<1 ms';
  return `${Number(value ?? 0).toLocaleString()} ${suffix}`;
}
export function requestDetail(request: ProxyRequest): HTMLElement {
  function group(title: string, values: [string, unknown][]) {
    const list = el('dl');
    for (const [label, value] of values)
      list.append(el('dt', label), el('dd', String(value ?? '—')));
    return el('div', el('h3', title), list);
  }
  const copy = button('Copy request ID', () => {
    if (!navigator.clipboard) {
      copy.textContent = 'Copy unavailable — select the ID below';
      return;
    }
    void navigator.clipboard.writeText(request.id).then(
      () => {
        copy.textContent = 'Request ID copied';
      },
      () => {
        copy.textContent = 'Copy unavailable — select the ID below';
      },
    );
  });
  const result = card(
    'Request details',
    group('Summary', [
      ['Started', dateTime(request.started, '—')],
      ['Ended', dateTime(request.ended, 'In progress')],
      ['Caller', request.caller_name || 'Unknown caller'],
      ['Route', request.route],
      ['Method', request.method],
      ['Local path', localPath(request)],
      ['Status', request.status || '—'],
      ['Outcome', outcomeLabel(request.outcome)],
    ]),
    group('Timing', [
      ['Headers', unit(request.header_ms, 'ms')],
      ['First byte', unit(request.first_byte_ms, 'ms')],
      ['Total', unit(request.total_ms, 'ms')],
    ]),
    group('Size', [
      ['Sent upstream', unit(request.sent, 'B')],
      ['Received upstream', unit(request.received, 'B')],
    ]),
    disclosure(
      'Identifiers',
      copy,
      group('Identifiers', [
        ['Request ID', request.id],
        ['Upstream request ID', request.upstream_id || '—'],
        ['Caller ID', request.caller_id],
        ['Peer', request.peer],
        ['Sequence', request.seq],
        ['Config revision', request.revision],
      ]),
    ),
    el(
      'small',
      'Metadata only. No request or response bodies or headers are captured. Errors and incomplete results may still mean the upstream performed work.',
    ),
  );
  result.className = 'request-detail';
  return result;
}

export function requestsView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const view = el('div');
  const caller = select([
    ['', 'All callers'],
    ...state.callers.map((c) => [c.id, c.name] as [string, string]),
  ]);
  const route = select([
    ['', 'All routes'],
    ...state.routes.map((r) => [r.name, r.name] as [string, string]),
  ]);
  const method = input();
  method.placeholder = 'Any method';
  const path = input();
  path.placeholder = '/anything';
  const outcome = select([['', 'All outcomes'], ...outcomes]);
  const since = input('', 'datetime-local');
  const until = input('', 'datetime-local');
  const id = input();
  const filters = el(
    'form',
    field('Caller', caller),
    field('Route', route),
    field('Method', method),
    field(
      'Path',
      path,
      'Relative /path or full /local-prefix/path; query values are never recorded.',
    ),
    field('Outcome', outcome),
    field('Since (local time)', since),
    field('Until (local time)', until),
    field('Request ID', id),
  );
  filters.className = 'filters';
  const apply = button('Refresh results');
  apply.type = 'submit';
  apply.className = 'primary';
  filters.append(apply);
  const filterPanel = disclosure('Filters (0)', filters);
  filterPanel.className = 'request-filters';
  filterPanel.open = window.matchMedia('(min-width: 641px)').matches;
  const summary = filterPanel.querySelector('summary')!;
  function updateCount() {
    summary.textContent = `Filters (${[caller, route, method, path, outcome, since, until, id].filter((c) => c.value.trim()).length})`;
  }
  filters.addEventListener('input', updateCount);
  filters.addEventListener('change', updateCount);
  const results = el('div');
  results.setAttribute('aria-label', 'Request results');
  const count = el('p');
  count.role = 'status';
  let active: RequestFilters = {};
  let next = 0;
  let loading = false;
  let selected: { row: HTMLElement; button: HTMLButtonElement; details: HTMLElement } | undefined;
  const older = button('Load older', () => {
    if (next) void load(true);
  });
  older.hidden = true;
  function readFilters(): RequestFilters {
    if (id.value.trim()) return { id: id.value.trim() };
    const values: RequestFilters = {
      caller: caller.value,
      route: route.value,
      method: method.value.trim().toUpperCase(),
      path: path.value,
      outcome: outcome.value,
    };
    if (since.value) values.since = String(new Date(since.value).getTime());
    if (until.value) values.until = String(new Date(until.value).getTime());
    if (values.since && values.until && Number(values.since) > Number(values.until))
      throw new Error('Since must be earlier than Until.');
    return values;
  }
  async function load(append: boolean) {
    if (loading) return;
    loading = true;
    older.disabled = true;
    await context.run(async () => {
      if (!append) active = readFilters();
      const page = await api.requests({ ...active, ...(append ? { before: String(next) } : {}) });
      if (!view.isConnected) return;
      if (!append) {
        results.replaceChildren();
        selected = undefined;
      }
      if (!page.requests.length && !append)
        results.append(
          el('p', 'No matching requests. Adjust filters or refresh after sending a request.'),
        );
      for (const request of page.requests) {
        const details = el('div');
        const row = el('article');
        row.className = 'request-row';
        const open = button(`${request.method} ${localPath(request)}`, () => {
          const closing = selected?.button === open;
          if (selected) {
            selected.row.classList.remove('selected');
            selected.button.setAttribute('aria-expanded', 'false');
            selected.details.replaceChildren();
          }
          selected = undefined;
          if (closing) {
            open.focus();
            return;
          }
          details.replaceChildren(requestDetail(request));
          row.classList.add('selected');
          open.setAttribute('aria-expanded', 'true');
          selected = { row, button: open, details };
          details.querySelector('h2')?.focus();
        });
        open.setAttribute('aria-expanded', 'false');
        details.id = `proxy-request-${request.seq}`;
        open.setAttribute('aria-controls', details.id);
        const chip = el('span', `${request.status || '—'} · ${outcomeLabel(request.outcome)}`);
        chip.className = `outcome ${request.status >= 400 || !['complete', 'active'].includes(request.outcome) ? 'outcome-error' : ''}`;
        row.append(
          open,
          el(
            'p',
            `${dateTime(request.started, '—')} · ${request.caller_name || request.caller_id || 'Unknown caller'} · ${request.route}`,
          ),
          el(
            'p',
            chip,
            ` · ${unit(request.total_ms, 'ms')} · ↑ ${unit(request.sent, 'B')} ↓ ${unit(request.received, 'B')}`,
          ),
          details,
        );
        results.append(row);
      }
      next = page.next;
      older.hidden = !next || !!active.id;
      count.textContent = `${results.querySelectorAll('article').length} requests shown${next ? ' · older requests available' : ''}`;
    }, filters);
    loading = false;
    older.disabled = false;
  }
  filters.addEventListener('submit', (event) => {
    event.preventDefault();
    void load(false);
  });
  const exportButton = button('Export JSON · up to 1,000', () => {
    void context.run(async () => {
      const chosen = readFilters();
      if (chosen.id) throw new Error('Clear Request ID to export a filtered request list.');
      download('proxy-requests.json', await api.requestExport(chosen), 'application/json');
      context.announce('Matching request metadata exported (up to 1,000 rows).');
    }, exportButton);
  });
  view.append(
    card(
      'Requests',
      el(
        'small',
        `Metadata only · ${state.retention_days ?? 30}-day retention. Errors may still mean upstream work happened; do not retry blindly.`,
      ),
      filterPanel,
      actions(exportButton),
    ),
    count,
    results,
    older,
  );
  queueMicrotask(() => {
    if (view.isConnected) void load(false);
  });
  return view;
}
