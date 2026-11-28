import {
  ProxyAPIError,
  type ProxyClient,
  type ProxyRequest,
  type ProxyState,
  type RequestFilters,
  type RequestPage,
} from '../src/api/proxy-client';
import { byteValue } from './bytes';
import { clipboardButton } from './clipboard';
import { requestCapture } from './debug-preview';
import { moneyLabel } from './home';
import { effortValue, finishValue, thinkingValue } from './request-insights';
import {
  actions,
  button,
  card,
  disclosure,
  download,
  el,
  field,
  input,
  select,
  titledValue,
  type UIContext,
} from './dom';

import {
  cacheValue,
  clientLabel,
  connectionOutcomes,
  countValue,
  dateLabel,
  dayKey,
  durationValue,
  isPartial,
  knownCount,
  normalizedInput,
  priceValue,
  requestGroups,
  statusValue,
  timestamp,
  timeValue,
  webSocketUpgrade,
  type LedgerValue,
} from './requests-ledger';

const outcomes: [string, string][] = [
  ['complete', 'Complete'],
  ['http_error', 'Upstream HTTP error'],
  ['transport_error', 'Connection error · result uncertain'],
  ['canceled_or_timeout', 'Canceled / timed out · result uncertain'],
  ['incomplete', 'Incomplete stream · result uncertain'],
  ...connectionOutcomes,
  ['active', 'In progress'],
  ['unknown', 'Unknown · result uncertain'],
];
export function outcomeLabel(value: string): string {
  return outcomes.find(([key]) => key === value)?.[1] ?? 'Unknown · result uncertain';
}
export function localPath(request: Pick<ProxyRequest, 'local_prefix' | 'path'>): string {
  return (request.local_prefix ?? '').replace(/\/+$/, '') + '/' + request.path.replace(/^\/+/, '');
}
function timing(value: number): string {
  if (!Number.isFinite(value) || value < 0) return 'Not reported';
  return value === 0 ? '<1 ms' : `${value.toLocaleString()} ms`;
}
function transferValues(request: ProxyRequest): [LedgerValue, LedgerValue] {
  const finished = request.outcome !== 'active';
  return [
    byteValue(finished ? request.sent : undefined, 'Bytes in (sent upstream)'),
    byteValue(finished ? request.received : undefined, 'Bytes out (received from upstream)'),
  ];
}
function tokenCount(value = -1, partial = false): string {
  return Number.isSafeInteger(value) && value >= 0
    ? `${partial ? '≥' : ''}${value.toLocaleString()}${partial ? ' · partial' : ''}`
    : 'Not reported';
}
export function requestDetail(
  request: ProxyRequest,
  context: Pick<UIContext, 'toast'>,
  capture?: HTMLElement,
): HTMLElement {
  function fields(values: [string, unknown][]) {
    const list = el('dl');
    for (const [label, value] of values)
      list.append(
        el('dt', label),
        el('dd', value instanceof HTMLElement ? value : String(value ?? '—')),
      );
    return list;
  }
  function group(title: string, values: [string, unknown][]) {
    return el('div', el('h3', title), fields(values));
  }
  const meta = request.request_meta;
  const usage = request.usage?.usage;
  const response = request.response_meta;
  const protocol = request.usage?.protocol || meta?.protocol;
  const connectionId = request.websocket_connection_id;
  const missing = meta?.state === 'observed' ? 'Not sent' : 'Not observed';
  const setting = (value: unknown) =>
    value === undefined || value === ''
      ? missing
      : typeof value === 'boolean'
        ? value
          ? 'On'
          : 'Off'
        : typeof value === 'number'
          ? Number.isFinite(value)
            ? value.toLocaleString()
            : 'Invalid'
          : value;
  const requested: [string, unknown][] = [
    ['Model', setting(meta?.model)],
    ['Effort', setting(meta?.reasoning_effort?.toLowerCase())],
    ['Output limit', setting(meta?.output_limit_mode || meta?.output_limit)],
    ['Tier', setting(meta?.service_tier)],
  ];
  const extra: [string, unknown][] = [
    ['Thinking', meta?.thinking_mode],
    ['Thinking budget', meta?.thinking_budget_mode || meta?.thinking_budget],
    ['Reasoning mode', meta?.reasoning_mode],
    ['Reasoning scope', meta?.reasoning_context],
    ['Summary', meta?.reasoning_summary],
    ['Temperature', meta?.temperature],
    ['Top-p', meta?.top_p],
    ['Stream', meta?.stream],
    ['Verbosity', meta?.verbosity],
    ['Tool choice', meta?.tool_choice],
    ['Parallel tools', meta?.parallel_tools],
    ['Declared tools', meta?.declared_tools],
    ['Multi-agent', meta?.multi_agent],
    ['Agent limit', meta?.agent_limit],
    ['Cache policy', meta?.cache_mode],
    ['Cache TTL', meta?.cache_ttl],
  ];
  requested.push(
    ...extra
      .filter(([, value]) => value !== undefined && value !== '')
      .map(([label, value]) => [label, setting(value)] satisfies [string, unknown]),
  );
  const plan = request.money?.billing === 'plan';
  const price = plan ? request.money?.api_equivalent : request.money;
  const responseRows: [string, unknown][] = [
    [
      'Finish',
      (response?.finish || response?.status || 'Not reported') +
        (response?.state === 'partial' ? ' · partial' : ''),
    ],
    ['Model', usage?.ambiguous ? 'Ambiguous' : usage?.model || 'Not reported'],
    ['Tier', usage?.ambiguous ? 'Ambiguous' : usage?.tier || 'Not reported'],
    ['First byte', timing(request.first_byte_ms)],
    ['Duration', timing(request.total_ms)],
    ['Cache write', tokenCount(usage?.cache_write, isPartial(request))],
    [plan ? 'API equivalent' : 'Price', titledValue(priceValue(request))],
  ];
  if (request.money?.billing === 'plan') responseRows.push(['Billing', moneyLabel(request.money)]);
  if (response?.incomplete_reason)
    responseRows.push(['Incomplete reason', response.incomplete_reason]);
  if (response?.choices && response.choices.length > 1)
    responseRows.push([
      'Choices',
      response.choices.map(({ index, reason }) => `${index}: ${reason}`).join(' · '),
    ]);
  for (const [label, value] of [
    ['Cache write · 5m', response?.cache_write_5m],
    ['Cache write · 1h', response?.cache_write_1h],
    ['Web search', response?.web_search_requests],
    ['Web fetch', response?.web_fetch_requests],
  ] as const)
    if (knownCount(value))
      responseRows.push([label, tokenCount(value, response?.state === 'partial')]);
  const copy = clipboardButton('Copy request ID', context, {
    read: () => request.id,
    copied: 'Request ID copied.',
    unavailable: 'Select and copy the request ID manually.',
  });
  const tokenValues = [
    [
      'Input',
      usage?.input,
      protocol === 'messages' || protocol === 'bedrock'
        ? 'Input excludes cache reads and writes'
        : 'Cache reads included in input',
    ],
    [protocol === 'gemini' ? 'Output · candidates' : 'Output', usage?.output, 'Reported output'],
    [
      protocol === 'gemini'
        ? 'Thinking · extra'
        : ['responses', 'chat_completions'].includes(protocol || '')
          ? 'Thinking · included'
          : 'Thinking',
      usage?.reasoning,
      protocol === 'gemini'
        ? 'Additional to candidate output'
        : ['responses', 'chat_completions'].includes(protocol || '')
          ? 'Already included in output'
          : 'Reported thinking tokens (when separately available)',
    ],
    ['Cache read', usage?.cached_input, 'Cache-read tokens'],
  ] as const;
  const tokenHeading = el('h3', 'Tokens');
  tokenHeading.className = 'visually-hidden';
  const tokenList = el('dl');
  const tokens = el('div', tokenHeading, tokenList);
  tokens.className = 'request-tokens';
  for (const [label, value, title] of tokenValues) {
    const metric = el('div', el('dt', label), el('dd', tokenCount(value, isPartial(request))));
    metric.title = title;
    tokenList.append(metric);
  }
  const grid = el('div', group('Requested', requested), group('Response', responseRows));
  grid.className = 'request-insights-grid';
  const [sent, received] = transferValues(request);
  const priceReason = ['unpriced', 'unmeasured', 'estimated_floor'].includes(price?.kind || '')
    ? price?.reason
        ?.replace(
          'Counterfactual API estimate only; not a bill, spending, or subscription savings.',
          '',
        )
        .replace(/\s+/g, ' ')
        .trim()
    : undefined;
  const metadata = el(
    'div',
    fields([
      ['Request ID', el('span', request.id, ' ', copy)],
      ...(connectionId
        ? [['WebSocket connection ID', connectionId] satisfies [string, unknown]]
        : []),
      ['Protocol', protocol || 'Not reported'],
      ['Endpoint', `${request.method} ${localPath(request)}`],
      ['Request settings', meta?.state || 'not_observed'],
      [
        'Evidence',
        (usage?.source || 'unknown') + (usage?.ambiguous ? ' · ambiguous model/tier' : ''),
      ],
      ...(response ? [['Diagnostics', response.state] satisfies [string, unknown]] : []),
      ['Status', request.status === 101 ? webSocketUpgrade : request.status || '—'],
      [
        connectionId ? 'Turn outcome' : 'Outcome',
        connectionId ? statusValue(request).word : outcomeLabel(request.outcome),
      ],
      ['Client', clientLabel(request)],
      ['Route', request.route],
      ['Started', timestamp(request.started)?.toString() ?? '—'],
      [
        'Ended',
        request.outcome === 'active'
          ? 'In progress'
          : (timestamp(request.ended)?.toString() ?? 'Not reported'),
      ],
      ['Headers', timing(request.header_ms)],
      ['Sent upstream', titledValue(sent)],
      ['Received upstream', titledValue(received)],
      ...(knownCount(usage?.total)
        ? [
            ['Total tokens', tokenCount(usage.total, isPartial(request))] satisfies [
              string,
              unknown,
            ],
          ]
        : []),
      ...(!connectionId && request.status === 101 && knownCount(usage?.turns)
        ? [['Observed turns', usage.turns] satisfies [string, unknown]]
        : []),
      ['Price state', price?.kind || 'Not reported'],
      ...(priceReason ? [['Price reason', priceReason] satisfies [string, unknown]] : []),
      ...(connectionId && capture
        ? [['Capture scope', 'WebSocket connection'] satisfies [string, unknown]]
        : []),
      ['Price source', price?.source || 'Not reported'],
      ['Price date', price?.asof || 'Not reported'],
      ['Upstream request ID', request.upstream_id || '—'],
      ['Client ID', request.caller_id],
      ['Peer', request.peer],
      ['Sequence', request.seq],
      ['Config revision', request.revision],
    ]),
  );
  metadata.className = 'request-meta';
  const result = card(
    'Request details',
    ...(tokenValues.some(([, value]) => knownCount(value)) ? [tokens] : []),
    grid,
    metadata,
  );
  if (capture) result.append(capture);
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

export function requestsView(state: ProxyState, api: ProxyClient, context: UIContext) {
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
  const apply = button('Apply filters');
  apply.type = 'submit';
  apply.className = 'primary';
  const clear = button('Clear', () => {
    void load(1, 'clear');
  });
  const refresh = button('Refresh', () => {
    void load(currentPage, 'refresh');
  });
  refresh.className = 'request-refresh';
  const filterPanel = disclosure('Filters', filters);
  filterPanel.className = 'request-filters';
  const summary = filterPanel.querySelector('summary')!;
  summary.className = 'request-filter';
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
  let applied: RequestFilters = {};
  let currentPage = 1;
  let total = 0;
  let shown = 0;
  let loading = false;
  let rendered: string | undefined;
  let interval: number | undefined;
  let polling: AbortController | undefined;
  let disposed = false;
  let selected: { row: HTMLElement; button: HTMLButtonElement; details: HTMLElement } | undefined;
  function updatePagination() {
    refresh.disabled = loading;
    const pages = Math.max(1, Math.ceil(total / pageSize));
    const first = shown ? (currentPage - 1) * pageSize + 1 : 0;
    const label = `${first.toLocaleString()}–${(first ? first + shown - 1 : 0).toLocaleString()} of ${total.toLocaleString()} request${total === 1 ? '' : 's'} · Page ${currentPage} of ${pages}`;
    if (count.textContent !== label) count.textContent = bottomCount.textContent = label;
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
    return Object.fromEntries(Object.entries(values).filter(([, value]) => value));
  }
  function renderRequests(requests: ProxyRequest[]) {
    results.replaceChildren();
    selected = undefined;
    if (!requests.length)
      results.append(
        el('p', 'No matching requests. Adjust filters or refresh after sending a request.'),
      );
    if (requests.length) {
      const head = el(
        'div',
        ...[
          'Time',
          'Status',
          'Model / endpoint',
          'Client / route',
          'Total in',
          'Out',
          'Cache',
          'Duration',
          'Price',
        ].map((label) => {
          const cell = el('span', label);
          if (label === 'Total in') cell.title = 'Total input includes cached tokens';
          if (label === 'Cache') cell.title = 'Cache-read share of total input';
          if (label === 'Price')
            cell.title =
              'Charged actual · ≈ estimated · ≥ partial lower bound · ≈ API is plan equivalent, not spend';
          return cell;
        }),
      );
      head.className = 'request-ledger-head';
      head.setAttribute('aria-hidden', 'true');
      results.append(head);
    }
    function closeSelected() {
      if (!selected) return;
      selected.row.classList.remove('selected');
      selected.button.setAttribute('aria-expanded', 'false');
      selected.details.replaceChildren();
      selected = undefined;
    }
    function renderRow(
      request: ProxyRequest,
      group: ProxyRequest[] = [request],
      nested = false,
    ): HTMLElement {
      const details = el('div');
      details.className = 'request-details';
      const row = el('article');
      row.className = 'request-row';
      const endpoint = `${request.method} ${localPath(request)}`;
      const servedModel = request.usage?.usage?.ambiguous ? undefined : request.usage?.usage?.model;
      const model = servedModel || request.request_meta?.model;
      const requestedModel = Boolean(model && !servedModel);
      const open = button('', () => {
        const closing = selected?.button === open;
        closeSelected();
        if (closing) {
          open.focus();
          return;
        }
        const captureId = request.websocket_connection_id || request.id;
        details.replaceChildren(
          requestDetail(request, context, requestCapture(api, context, captureId)),
        );
        row.classList.add('selected');
        open.setAttribute('aria-expanded', 'true');
        selected = { row, button: open, details };
        details.querySelector('h2')?.focus();
      });
      const modelLabel = el('span', model || endpoint);
      modelLabel.className = 'request-model-label';
      open.append(modelLabel);
      open.className = 'request-open';
      open.setAttribute(
        'aria-label',
        requestedModel ? `${endpoint} · Requested model: ${model}` : endpoint,
      );
      open.title = model
        ? `${requestedModel ? 'Requested model: ' : ''}${model} · ${endpoint}`
        : endpoint;
      open.setAttribute('aria-expanded', 'false');
      details.id = `proxy-request-${request.seq}${nested ? '-group' : ''}`;
      if (model) {
        modelLabel.id = `${details.id}-model`;
        open.setAttribute('aria-describedby', modelLabel.id);
      }
      open.setAttribute('aria-controls', details.id);
      details.addEventListener('keydown', (event) => {
        if (event.key === 'Escape') {
          event.stopPropagation();
          closeSelected();
          open.focus();
        }
      });
      const identity = el('div', open);
      identity.className = 'request-model';
      const summary = [effortValue(request), finishValue(request)].filter(Boolean);
      if (summary.length) {
        const meta = el('div', summary.join(' · '));
        meta.className = 'request-summary';
        identity.append(meta);
      }
      const status = statusValue(request);
      const chip = el('span');
      if (status.code) {
        const code = el('span', status.code);
        code.className = 'request-status-code';
        chip.append(code, status.word ? ` ${status.word}` : '');
      } else {
        chip.textContent = status.text;
      }
      chip.className = `request-status status-${status.tone}`;
      chip.title = status.title;
      chip.setAttribute('aria-label', status.title);
      function cell(name: string, value: LedgerValue): HTMLElement {
        const node = titledValue(value);
        node.className = `request-${name} request-number`;
        return node;
      }
      const time = el('time', timeValue(request.started).text);
      time.className = 'request-time';
      time.title = timeValue(request.started).title;
      const date = timestamp(request.started);
      if (date) time.dateTime = date.toISOString();
      const partial = isPartial(request);
      const tokenInput = countValue(
        normalizedInput(request),
        'Total input tokens (including cached tokens)',
        partial,
      );
      const tokenOutput = ['unknown', 'not_applicable'].includes(request.usage?.usage?.source || '')
        ? { text: '—', title: 'Output tokens: unknown · usage evidence unavailable' }
        : countValue(request.usage?.usage?.output, 'Output tokens', partial);
      const fallback = tokenInput.text === '—' && tokenOutput.text === '—';
      const [input, output] = fallback ? transferValues(request) : [tokenInput, tokenOutput];
      const latest = ' · Latest request, not aggregate';
      if (fallback && group.length > 1) {
        input.title += latest;
        output.title += latest;
      }
      const callerName = clientLabel(request);
      const client = el('span', el('span', callerName), el('span', request.route));
      client.className = 'request-client';
      client.title = `Client: ${callerName} · Route: ${request.route}`;
      client.setAttribute('aria-label', client.title);
      const duration = cell('duration', durationValue(request.total_ms));
      if (group.length > 1) {
        time.title += latest;
        time.setAttribute('aria-label', time.title);
        duration.title += latest;
        duration.setAttribute('aria-label', duration.title);
      }
      const outputCell = cell('output', output);
      const thinking = thinkingValue(request);
      if (thinking && !fallback) {
        const subline = titledValue(thinking);
        subline.className = 'request-thinking';
        outputCell.append(subline);
      }
      function clone(node: HTMLElement, className: string): HTMLElement {
        const copy = node.cloneNode(true) as HTMLElement;
        copy.className = className;
        return copy;
      }
      const stamp = el(
        'div',
        clone(time, 'request-stamp-time'),
        clone(chip, `request-stamp-status status-${status.tone}`),
      );
      stamp.className = 'request-stamp';
      const compactClient = client.cloneNode(true) as HTMLElement;
      compactClient.className = 'request-compact-client';
      const compactUsage = cell('mobile-usage', {
        text: input.text === '—' && output.text === '—' ? '—' : `${input.text}→${output.text}`,
        title: `${input.title}; ${output.title}`,
      });
      const compactTokens = el('span', compactUsage);
      compactTokens.className = 'request-compact-tokens';
      if (thinking && !fallback) {
        const subline = titledValue(thinking);
        subline.className = 'request-compact-thinking';
        compactTokens.append(subline);
      }
      const compactFacts = el(
        'div',
        compactClient,
        clone(chip, `request-compact-status status-${status.tone}`),
        clone(time, 'request-compact-time'),
        clone(duration, 'request-compact-duration'),
        compactTokens,
      );
      compactFacts.className = 'request-compact-facts';
      row.append(
        time,
        chip,
        identity,
        client,
        cell('input', input),
        outputCell,
        cell('cache', cacheValue(request)),
        duration,
        cell('price', priceValue(request)),
        stamp,
        compactFacts,
        details,
      );
      if (group.length > 1) {
        const children = el('div');
        children.className = 'request-group-rows request-details';
        children.id = `proxy-request-group-${request.seq}`;
        const repeat = button(`×${group.length}`, () => {
          const expanded = repeat.getAttribute('aria-expanded') === 'true';
          if (selected && children.contains(selected.row)) closeSelected();
          children.replaceChildren(
            ...(expanded ? [] : group.map((item) => renderRow(item, [item], true))),
          );
          repeat.setAttribute('aria-expanded', String(!expanded));
          repeat.setAttribute(
            'aria-label',
            `${expanded ? 'Expand' : 'Collapse'} ${group.length} repeated requests`,
          );
          repeat.focus();
        });
        repeat.className = 'request-repeat';
        repeat.title =
          'Consecutive identical unmeasured HTTP errors; time and duration show the latest request, not totals';
        repeat.setAttribute('aria-label', `Expand ${group.length} repeated requests`);
        repeat.setAttribute('aria-expanded', 'false');
        repeat.setAttribute('aria-controls', children.id);
        identity.append(repeat);
        row.append(children);
      }
      return row;
    }
    let previousDay = '';
    for (const group of requestGroups(requests)) {
      // The API orders newest first; keep this explicit even for imported rows.
      const request = group.reduce((latest, item) =>
        (timestamp(item.started)?.getTime() ?? -Infinity) >
        (timestamp(latest.started)?.getTime() ?? -Infinity)
          ? item
          : latest,
      );
      const day = dayKey(request.started);
      if (day !== previousDay) {
        const heading = el('h3', dateLabel(request.started));
        heading.className = 'request-date-group';
        results.append(heading);
        previousDay = day;
      }
      results.append(renderRow(request, group));
    }
  }

  function showPage(page: RequestPage, requested: number): boolean {
    const nextPage = page.page ?? requested;
    const nextTotal = page.total ?? page.requests.length;
    const paginationChanged =
      currentPage !== nextPage || total !== nextTotal || shown !== page.requests.length;
    currentPage = nextPage;
    total = nextTotal;
    shown = page.requests.length;
    const signature = JSON.stringify(page.requests);
    if (signature !== rendered) {
      rendered = signature;
      renderRequests(page.requests);
    }
    return paginationChanged;
  }

  function stopPoll() {
    polling?.abort();
    polling = undefined;
  }

  function canPoll() {
    return !disposed && view.isConnected && !document.hidden && currentPage === 1 && !loading;
  }

  async function poll() {
    if (!canPoll() || polling) return;
    const controller = new AbortController();
    polling = controller;
    try {
      const page = await api.requests(
        { ...applied, page: '1', page_size: String(pageSize) },
        controller.signal,
      );
      // Don't close details, collapse groups, or pull focus out of the ledger.
      if (
        !controller.signal.aborted &&
        canPoll() &&
        !selected &&
        !results.contains(document.activeElement) &&
        !results.querySelector('.request-repeat[aria-expanded="true"]') &&
        !topControls.contains(document.activeElement) &&
        !bottomControls.contains(document.activeElement) &&
        showPage(page, 1)
      ) {
        updatePagination();
      }
    } catch (error) {
      // Recover expired admin sessions, but retry transient failures quietly.
      if (
        !controller.signal.aborted &&
        canPoll() &&
        error instanceof ProxyAPIError &&
        error.status === 401
      ) {
        await context.run(() => {
          throw error;
        });
      }
    } finally {
      if (polling === controller) polling = undefined;
    }
  }

  async function load(pageNumber: number, intent: 'page' | 'apply' | 'clear' | 'refresh' = 'page') {
    if (loading || pageNumber < 1) return;
    stopPoll();
    loading = true;
    results.setAttribute('aria-busy', 'true');
    updatePagination();
    const success = await context.run(async () => {
      const chosen = intent === 'apply' ? readFilters() : intent === 'clear' ? {} : applied;
      const page = await api.requests({
        ...chosen,
        page: String(pageNumber),
        page_size: String(pageSize),
      });
      if (disposed || !view.isConnected) return;
      showPage(page, pageNumber);
      if (chosen !== applied) {
        applied = chosen;
        const appliedCount = Object.keys(applied).length;
        summary.textContent = appliedCount ? `Filters (${appliedCount})` : 'Filters';
      }
      if (intent === 'clear') filters.reset();
    }, filters);
    loading = false;
    if (disposed || !view.isConnected) return;
    results.setAttribute('aria-busy', 'false');
    updatePagination();
    if (success && intent === 'page' && view.isConnected) count.focus();
  }
  filters.addEventListener('submit', (event) => {
    event.preventDefault();
    void load(1, 'apply');
  });
  const exportButton = button('Export JSON · up to 1,000', () => {
    void context.run(async () => {
      if (applied.id)
        throw new Error('Clear the applied Request ID to export a filtered request list.');
      download('proxy-requests.json', await api.requestExport(applied), 'application/json');
      context.toast('Matching request metadata exported (up to 1,000 rows).', 'success');
    }, exportButton);
  });
  filters.append(actions(apply, clear, exportButton));
  const zone = el(
    'small',
    `Times: ${Intl.DateTimeFormat().resolvedOptions().timeZone || 'local timezone'}`,
  );
  zone.className = 'request-timezone';
  const header = card(
    'Requests',
    el('small', `${state.retention_days ?? 30}-day history`),
    zone,
    filterPanel,
    refresh,
  );
  header.className = 'requests-header';
  const legend = el(
    'small',
    'Input tokens include cache · Bytes marked B or ~ · API = plan estimate, not spend',
  );
  legend.className = 'request-ledger-legend';
  view.append(header, topPager, legend, results, bottomPager);
  updatePagination();
  queueMicrotask(() => {
    if (disposed || !view.isConnected) return;
    void load(1, 'refresh');
    interval = window.setInterval(() => void poll(), 1000);
  });
  return {
    element: view,
    dispose() {
      disposed = true;
      window.clearInterval(interval);
      stopPoll();
    },
  };
}
