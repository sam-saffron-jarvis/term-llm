import {
  ProxyAPIError,
  type ProxyClient,
  type ProxyState,
  type ProxyUsageProvider,
  type ProxyUsageSnapshot,
  type ProxyUsageWindow,
} from '../src/api/proxy-client';
import { button, dateTime, el, input, type UIContext } from './dom';
import {
  formatUsageDuration,
  usageMinute,
  usagePercent,
  usageTimestamp,
  usageWindowLabel,
  usageWindowTiming,
} from './usage-projection';

const columns = ['Provider', 'Limit group', 'Window', 'Used', 'Resets in', 'At this pace'];
const intervalDefault = 15;
const intervalMin = 10;
const intervalMax = 1440;
const refreshCheckLimit = 20;
const refreshCheckDelay = 1_000;
const requestTimeout = 15_000;
const usageProviderNames: Record<string, string> = {
  'agy-bin': 'Antigravity',
  'claude-bin': 'Claude Code',
  chatgpt: 'ChatGPT',
  grok: 'Grok',
  'opencode-go': 'OpenCode Go',
  openrouter: 'OpenRouter',
};

type UpdateRow = (now: number) => void;

function styled<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className: string,
  ...children: (Node | string)[]
): HTMLElementTagNameMap[K] {
  const node = el(tag, ...children);
  node.className = className;
  return node;
}

function hidden(text: string) {
  return styled('span', 'visually-hidden', text);
}

function dash(label: string) {
  const glyph = el('span', '—');
  glyph.setAttribute('aria-hidden', 'true');
  return el('span', glyph, hidden(label));
}

function scopedCell(headers: string, ...children: (Node | string)[]) {
  const cell = el('td', ...children);
  cell.headers = headers;
  return cell;
}

function setText(element: HTMLElement, text: string) {
  if (element.textContent !== text) element.textContent = text;
}

function windowReading(window: ProxyUsageWindow, timing: ReturnType<typeof usageWindowTiming>) {
  if (timing.expired) {
    return { reading: styled('span', 'usage-previous', 'Previous window') };
  }
  const { pacePercent } = timing;
  const reading = styled('div', 'usage-reading');
  const used = usagePercent(window);
  let bar: HTMLElement | undefined;
  if (used === undefined) {
    reading.classList.add('usage-reading-text');
    reading.append(window.disabled ? 'Disabled' : window.detail || 'Unavailable');
  } else {
    const fill = styled('span', 'usage-bar-fill');
    fill.style.width = `${used}%`;
    bar = styled('span', 'usage-bar', fill);
    bar.setAttribute('aria-hidden', 'true');
    const text = el('span', el('strong', `${Math.round(used)}%`), hidden(' used'));
    if (pacePercent !== undefined) {
      const marker = styled('span', 'usage-bar-pace');
      marker.style.left = `${pacePercent}%`;
      bar.append(marker);
    }
    reading.append(bar, text);
  }
  return { reading, bar };
}

function windowRow(
  window: ProxyUsageWindow,
  provider: string,
  group: string,
  id: string,
  snapshotAt: number | undefined,
  isStale: (now: number) => boolean,
) {
  const name = el('th', styled('span', 'usage-window-label', usageWindowLabel(window)));
  name.id = id;
  name.scope = 'row';
  name.headers = `usage-col-2 ${provider} ${group}`;
  const rowHeaders = `${provider} ${group} ${id}`;
  const readingCell = scopedCell(`${rowHeaders} usage-col-3`);
  const resetTime = el('time');
  const resetCell = scopedCell(`${rowHeaders} usage-col-4`, resetTime);
  const noReset = dash('Reset time unavailable');
  const noForecast = dash('No forecast');
  const forecastCell = scopedCell(`${rowHeaders} usage-col-5`, noForecast);
  forecastCell.className = 'usage-forecast';
  const detail =
    window.detail && (window.disabled || usagePercent(window) !== undefined)
      ? styled('small', 'usage-detail', window.detail)
      : undefined;
  let previousExpired: boolean | undefined;
  let previousPace: number | undefined;
  let bar: HTMLElement | undefined;
  function update(now: number) {
    const timing = usageWindowTiming(window, snapshotAt, now, isStale(now));
    if (
      !readingCell.firstChild ||
      previousExpired !== timing.expired ||
      previousPace !== timing.pacePercent
    ) {
      const rendered = windowReading(window, timing);
      readingCell.replaceChildren(rendered.reading);
      if (detail && !timing.expired) readingCell.append(detail);
      previousExpired = timing.expired;
      previousPace = timing.pacePercent;
      bar = rendered.bar;
    }
    if (timing.resetAt === undefined) {
      if (resetCell.firstChild !== noReset) resetCell.replaceChildren(noReset);
    } else {
      resetTime.dateTime = new Date(timing.resetAt).toISOString();
      setText(
        resetTime,
        timing.expired ? 'Reset passed' : formatUsageDuration(timing.resetAt - now),
      );
    }
    forecastCell.dataset.tone = timing.forecast?.kind ?? 'quiet';
    if (bar) bar.dataset.tone = forecastCell.dataset.tone;
    if (timing.forecast) setText(forecastCell, timing.forecast.text);
    else if (forecastCell.firstChild !== noForecast) forecastCell.replaceChildren(noForecast);
  }
  return { row: el('tr', name, readingCell, resetCell, forecastCell), update };
}

function providerRows(
  provider: ProxyUsageProvider,
  description: string,
  id: string,
  isStale: (snapshotAt: number | undefined, now: number) => boolean,
  updates: UpdateRow[],
) {
  const body = el('tbody');
  const groups = provider.report?.groups ?? [];
  // The provider ID names the row; its label only describes it (see providerName).
  const heading = el('th', provider.id);
  heading.className = 'usage-provider';
  heading.scope = 'rowgroup';
  heading.id = id;
  if (description && provider.id !== description)
    heading.append(styled('small', 'usage-detail', description));
  if (provider.route) heading.append(styled('small', 'usage-detail', provider.route));
  if (provider.report?.plan) heading.append(styled('small', 'usage-detail', provider.report.plan));
  const snapshotAt = usageTimestamp(provider.updated_at);
  const staleLabel = styled('small', 'usage-provider-state', 'Stale snapshot');
  staleLabel.hidden = true;
  const stale = (now: number) => Boolean(provider.error) || isStale(snapshotAt, now);
  if (provider.report) {
    if (snapshotAt !== undefined) {
      const time = styled('time', 'usage-detail', `Updated ${dateTime(snapshotAt)}`);
      time.dateTime = new Date(snapshotAt).toISOString();
      heading.append(time);
    } else heading.append(styled('small', 'usage-detail', 'Update time unknown'));
    heading.append(staleLabel);
    updates.push((now) => {
      staleLabel.hidden = !stale(now);
    });
  }
  if (provider.error) heading.append(styled('small', 'usage-provider-state', provider.error));
  for (const [index, group] of groups.entries()) {
    const groupCell = styled('th', 'usage-group', group.name || 'Limit');
    const groupId = `${id}-group-${index}`;
    groupCell.id = groupId;
    groupCell.headers = `usage-col-1 ${id}`;
    if (group.detail) groupCell.append(styled('small', 'usage-detail', group.detail));
    const windows = group.windows ?? [];
    groupCell.rowSpan = windows.length || 1;
    if (!windows.length) {
      const empty = scopedCell(`${id} ${groupId}`, 'No limits reported.');
      empty.colSpan = 4;
      body.append(el('tr', groupCell, empty));
    }
    for (const [index, window] of windows.entries()) {
      const item = windowRow(window, id, groupId, `${groupId}-window-${index}`, snapshotAt, stale);
      if (index === 0) item.row.prepend(groupCell);
      body.append(item.row);
      updates.push(item.update);
    }
  }
  if (!body.rows.length) {
    const message = provider.report
      ? 'No limits reported.'
      : provider.error
        ? 'Usage unavailable.'
        : 'No snapshot yet.';
    const empty = scopedCell(id, message);
    empty.colSpan = 5;
    body.append(el('tr', empty));
  }
  heading.rowSpan = body.rows.length;
  body.rows[0].prepend(heading);
  return body;
}

export function usageView(state: ProxyState, api: ProxyClient, context: UIContext) {
  const view = styled('div', 'usage-view');
  const heading = el('h2', 'Usage limits');
  heading.tabIndex = -1;
  const count = styled('p', 'usage-count');
  const status = styled('p', 'usage-status');
  status.setAttribute('role', 'status');

  const interval = input(String(intervalDefault), 'number');
  interval.min = String(intervalMin);
  interval.max = String(intervalMax);
  interval.step = '1';
  interval.required = true;
  const save = button('Save interval');
  save.type = 'submit';
  const settings = styled('form', 'usage-settings', el('label', 'Interval (min)', interval), save);
  const refresh = button('Refresh', () => void load((signal) => api.refreshUsage(signal), true));
  refresh.setAttribute('aria-label', 'Refresh usage');
  settings.addEventListener('submit', (event) => {
    event.preventDefault();
    const minutes = interval.valueAsNumber;
    if (!interval.checkValidity() || !Number.isInteger(minutes) || minutes === savedInterval)
      return;
    void load(async (signal) => {
      await api.setUsageInterval(minutes, signal);
      if (!signal.aborted) setIntervalValue(minutes);
    });
  });
  const header = styled(
    'header',
    'usage-header',
    el('div', heading, count),
    styled('div', 'usage-controls', settings, refresh),
  );
  const table = styled('table', 'usage-table');
  table.setAttribute('aria-label', 'Native usage limits');
  const legend = styled(
    'div',
    'usage-legend',
    styled('span', 'usage-legend-used', 'Used'),
    styled('span', 'usage-legend-pace', 'Even pace'),
  );
  const scroll = styled('div', 'usage-table-scroll', table);
  scroll.tabIndex = 0;
  scroll.setAttribute('role', 'region');
  scroll.setAttribute('aria-label', 'Usage limits table');
  const matrix = styled('div', 'usage-matrix', legend, scroll);
  const foot = styled(
    'p',
    'usage-footnote',
    '— No forecast · Pace and estimates are based on the last provider snapshot.',
  );
  const notes = styled('div', 'usage-report-notes');
  const tableHead = el(
    'thead',
    el(
      'tr',
      ...columns.map((name, index) => {
        const cell = el('th', name);
        cell.scope = 'col';
        cell.id = `usage-col-${index}`;
        return cell;
      }),
    ),
  );
  view.append(header, status, matrix, foot, notes);
  matrix.hidden = foot.hidden = true;

  const providerLabels = new Map(state.providers.map(({ id, label }) => [id, label]));
  function providerDescription(id: string) {
    return (
      providerLabels.get(id) ||
      state.presets?.find((preset) => preset.id === id)?.label ||
      (Object.hasOwn(usageProviderNames, id) ? usageProviderNames[id] : id)
    );
  }
  let updates: UpdateRow[] = [];
  let savedInterval = intervalDefault;
  let refreshing = false;
  let quickChecks = 0;
  let readError = '';
  let actionError = '';
  let loaded = false;
  let disposed = false;
  let providersKey: string | undefined;
  let work: AbortController | undefined;
  let mutating = false;
  let pollTimer: number | undefined;
  let clockTimer: number | undefined;

  function setIntervalValue(value: number) {
    if (interval.value === String(savedInterval)) interval.value = String(value);
    savedInterval = value;
  }

  function accept(snapshot: ProxyUsageSnapshot) {
    loaded = true;
    readError = actionError = '';
    const value = snapshot.interval_minutes;
    if (Number.isInteger(value) && value >= intervalMin && value <= intervalMax)
      setIntervalValue(value);
    if (snapshot.refreshing && !refreshing) quickChecks = refreshCheckLimit;
    refreshing = snapshot.refreshing;
    const providers = snapshot.providers ?? [];
    const key = JSON.stringify(providers);
    if (providersKey === key) return;
    providersKey = key;
    let limits = 0;
    for (const provider of providers) {
      for (const group of provider.report?.groups ?? []) limits += group.windows?.length ?? 0;
    }
    const providerCount = new Set(providers.map(({ id }) => id)).size;
    setText(
      count,
      providers.length
        ? `${providerCount} ${providerCount === 1 ? 'provider' : 'providers'} · ${limits} ${limits === 1 ? 'limit' : 'limits'}`
        : '',
    );
    updates = [];
    table.replaceChildren(
      tableHead,
      ...providers.map((provider, index) =>
        providerRows(
          provider,
          providerDescription(provider.id),
          `usage-provider-${index}`,
          (snapshotAt, now) =>
            Boolean(readError) ||
            snapshotAt === undefined ||
            now - snapshotAt > 2 * savedInterval * usageMinute,
          updates,
        ),
      ),
    );
    notes.replaceChildren(
      ...providers.flatMap(({ id, route, report }) =>
        report?.detail
          ? [el('p', el('strong', `${id}${route ? ` · ${route}` : ''}: `), report.detail)]
          : [],
      ),
    );
    matrix.hidden = foot.hidden = providers.length === 0;
  }

  function update() {
    const now = Date.now();
    for (const updateRow of updates) updateRow(now);
    refresh.disabled = mutating || refreshing;
    save.disabled = mutating || interval.value === String(savedInterval);
    refresh.textContent = refreshing ? 'Refreshing…' : 'Refresh';
    status.dataset.tone = readError || actionError ? 'error' : 'quiet';
    const message =
      actionError ||
      readError ||
      (refreshing
        ? quickChecks
          ? 'Fetching provider usage…'
          : 'Refresh still running.'
        : !loaded
          ? 'Loading usage…'
          : matrix.hidden
            ? 'No native usage reported.'
            : '');
    setText(status, message);
  }
  interval.addEventListener('input', update);

  function schedule() {
    if (disposed || document.hidden) return;
    const quick = refreshing && quickChecks > 0;
    pollTimer = window.setTimeout(
      () => {
        if (quick) quickChecks--;
        void load();
      },
      quick ? refreshCheckDelay : usageMinute,
    );
  }

  async function load(action?: (signal: AbortSignal) => Promise<void>, manualRefresh = false) {
    if (disposed || mutating || (!action && (document.hidden || work))) return;
    // User actions supersede reads. A superseded read must not publish its
    // snapshot or reset the new request's controls and polling timer.
    work?.abort();
    window.clearTimeout(pollTimer);
    const active = new AbortController();
    work = active;
    mutating = Boolean(action);
    if (action) actionError = '';
    const signal = AbortSignal.any([active.signal, AbortSignal.timeout(requestTimeout)]);
    let reading = !action;
    update();
    try {
      if (action) {
        await action(signal);
        if (signal.aborted) return;
        reading = true;
        mutating = false;
        update();
      }
      const snapshot = await api.usage(signal);
      if (!signal.aborted) accept(snapshot);
    } catch (error) {
      if (active.signal.aborted) return;
      if (error instanceof ProxyAPIError && error.status === 401) {
        await context.run(() => {
          throw error;
        });
        return;
      }
      if (reading) {
        refreshing = false;
        quickChecks = 0;
        actionError = '';
        readError = loaded
          ? 'Could not load usage. Showing last loaded data.'
          : 'Could not load usage.';
      } else if (manualRefresh && error instanceof ProxyAPIError && error.status === 429)
        actionError = 'Usage refresh is on cooldown. Try again in a minute.';
      else
        actionError = manualRefresh
          ? 'Could not refresh usage.'
          : 'Could not save refresh interval.';
    } finally {
      if (work === active) {
        work = undefined;
        mutating = false;
        if (!disposed) {
          update();
          schedule();
        }
      }
    }
  }

  function onVisibility() {
    window.clearTimeout(pollTimer);
    window.clearInterval(clockTimer);
    if (disposed || document.hidden) return;
    update();
    void load();
    clockTimer = window.setInterval(update, 30_000);
  }

  document.addEventListener('visibilitychange', onVisibility);
  update();
  queueMicrotask(onVisibility);
  return {
    element: view,
    dispose() {
      disposed = true;
      document.removeEventListener('visibilitychange', onVisibility);
      window.clearTimeout(pollTimer);
      window.clearInterval(clockTimer);
      work?.abort();
    },
  };
}
