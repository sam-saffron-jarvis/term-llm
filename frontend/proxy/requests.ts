import type {
  ProxyClient,
  ProxyRequest,
  ProxyState,
  RequestFilters,
} from '../src/api/proxy-client';
import { moneyAmount, moneyLabel } from './home';
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
    group('Money', [
      ['Cost', moneyLabel(request.money)],
      [
        'Price source',
        request.money?.billing === 'pay_as_you_go'
          ? request.money.source || 'Not available'
          : 'Not applicable',
      ],
      [
        'Price date',
        request.money?.billing === 'pay_as_you_go'
          ? request.money.asof || 'Not available'
          : 'Not applicable',
      ],
      [
        'Note',
        (request.money?.billing === 'plan'
          ? 'Plan allowance and possible extra usage are not reported. Configured plan activity does not establish zero marginal billing.'
          : request.money?.billing === 'pay_as_you_go'
            ? request.money.reason
            : undefined) ||
          'Estimates are before tax, fees, discounts and credits. Incomplete or canceled work can still cost money.',
      ],
    ]),
    ...(request.money?.billing === 'pay_as_you_go' && request.money.rate
      ? [
          group('Price used', [
            ['Provider / model', `${request.money.rate.provider} / ${request.money.rate.model}`],
            ['Service / context tier', request.money.rate.tier || 'Default (exact match)'],
            [
              'Input / million tokens',
              moneyAmount(request.money.rate.input_micros_per_million, request.money.rate.currency),
            ],
            [
              'Output / million tokens',
              moneyAmount(
                request.money.rate.output_micros_per_million,
                request.money.rate.currency,
              ),
            ],
            [
              'Cache read / million',
              request.money.rate.cache_read_micros_per_million === undefined
                ? 'Not priced'
                : moneyAmount(
                    request.money.rate.cache_read_micros_per_million,
                    request.money.rate.currency,
                  ),
            ],
            [
              'Cache write / million',
              request.money.rate.cache_write_micros_per_million === undefined
                ? 'Not priced'
                : moneyAmount(
                    request.money.rate.cache_write_micros_per_million,
                    request.money.rate.currency,
                  ),
            ],
          ]),
        ]
      : []),
    group('Summary', [
      ['Started', dateTime(request.started, '—')],
      ['Ended', dateTime(request.ended, 'In progress')],
      ['Client', request.caller_name || 'Unknown client'],
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
        ['Client ID', request.caller_id],
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

export function requestPageNumbers(current: number, total: number): (number | '…')[] {
  const visible = new Set([1, 2, total - 1, total]);
  for (let page = current - 1; page <= current + 1; page++) visible.add(page);
  if (current <= 4) for (let page = 3; page <= 5; page++) visible.add(page);
  if (current >= total - 3) for (let page = total - 4; page < total; page++) visible.add(page);
  const numbers = [...visible].filter((page) => page >= 1 && page <= total).sort((a, b) => a - b);
  const result: (number | '…')[] = [];
  for (const page of numbers) {
    const previous = result.at(-1);
    if (typeof previous === 'number' && page > previous + 1) {
      result.push(page === previous + 2 ? previous + 1 : '…');
    }
    result.push(page);
  }
  return result;
}

export function requestsView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const view = el('div');
  view.className = 'requests-view';
  const caller = select([
    ['', 'All clients'],
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
    field('Client', caller),
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
  const filterPanel = disclosure('Filters (0)', filters);
  filterPanel.className = 'request-filters';
  const summary = filterPanel.querySelector('summary')!;
  function updateCount() {
    summary.textContent = `Filters (${[caller, route, method, path, outcome, since, until, id].filter((c) => c.value.trim()).length})`;
  }
  filters.addEventListener('input', updateCount);
  filters.addEventListener('change', updateCount);
  const results = el('div');
  results.className = 'request-list';
  results.setAttribute('aria-label', 'Request results');
  const count = el('p');
  count.role = 'status';
  count.tabIndex = -1;
  const bottomCount = el('p');
  const topControls = actions();
  const bottomControls = actions();
  const topPager = el('div', count, topControls);
  const bottomPager = el('div', bottomCount, bottomControls);
  for (const [pager, name] of [
    [topPager, 'Request pages'],
    [bottomPager, 'Request pages (bottom)'],
  ] as const) {
    pager.className = 'request-pagination';
    pager.role = 'navigation';
    pager.setAttribute('aria-label', name);
  }
  const pageSize = 25;
  let active: RequestFilters = {};
  let currentPage = 1;
  let total = 0;
  let shown = 0;
  let loading = false;
  let selected: { row: HTMLElement; button: HTMLButtonElement; details: HTMLElement } | undefined;
  function updatePagination() {
    const pages = Math.max(1, Math.ceil(total / pageSize));
    const first = shown ? (currentPage - 1) * pageSize + 1 : 0;
    count.textContent = `${first.toLocaleString()}–${(first ? first + shown - 1 : 0).toLocaleString()} of ${total.toLocaleString()} request${total === 1 ? '' : 's'} · Page ${currentPage} of ${pages}`;
    bottomCount.textContent = count.textContent;
    for (const controls of [topControls, bottomControls]) {
      const previous = button('Previous', () => {
        void load(currentPage - 1);
      });
      previous.disabled = loading || currentPage <= 1;
      const next = button('Next', () => {
        void load(currentPage + 1);
      });
      next.disabled = loading || currentPage >= pages;
      controls.replaceChildren(previous);
      for (const number of requestPageNumbers(currentPage, pages)) {
        if (number === '…') {
          controls.append(el('span', number));
          continue;
        }
        const item = button(String(number), () => {
          void load(number);
        });
        item.setAttribute('aria-label', `Page ${number}`);
        if (number === currentPage) item.setAttribute('aria-current', 'page');
        item.disabled = loading || number === currentPage;
        controls.append(item);
      }
      controls.append(next);
    }
  }
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
  async function load(pageNumber = 1, refresh = false) {
    if (loading || pageNumber < 1) return;
    loading = true;
    results.setAttribute('aria-busy', 'true');
    updatePagination();
    const success = await context.run(async () => {
      const chosen = refresh ? readFilters() : active;
      const page = await api.requests({
        ...chosen,
        page: String(pageNumber),
        page_size: String(pageSize),
      });
      if (!view.isConnected) return;
      active = chosen;
      currentPage = page.page ?? pageNumber;
      total = page.total ?? page.requests.length;
      shown = page.requests.length;
      results.replaceChildren();
      selected = undefined;
      if (!page.requests.length)
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
            `${dateTime(request.started, '—')} · ${request.caller_name || request.caller_id || 'Unknown client'} · ${request.route}`,
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
    }, filters);
    loading = false;
    results.setAttribute('aria-busy', 'false');
    updatePagination();
    if (success && !refresh && view.isConnected) count.focus();
  }
  filters.addEventListener('submit', (event) => {
    event.preventDefault();
    void load(1, true);
  });
  const exportButton = button('Export JSON · up to 1,000', () => {
    void context.run(async () => {
      const chosen = readFilters();
      if (chosen.id) throw new Error('Clear Request ID to export a filtered request list.');
      download('proxy-requests.json', await api.requestExport(chosen), 'application/json');
      context.announce('Matching request metadata exported (up to 1,000 rows).');
    }, exportButton);
  });
  filters.append(actions(apply, exportButton));
  const header = card(
    'Requests',
    el('small', `${state.retention_days ?? 30}-day history`),
    filterPanel,
  );
  header.className = 'requests-header';
  view.append(header, topPager, results, bottomPager);
  updatePagination();
  queueMicrotask(() => {
    if (view.isConnected) void load(1, true);
  });
  return view;
}
