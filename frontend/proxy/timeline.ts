import type {
  ChartGroup,
  ChartRange,
  MoneyTimeline,
  TimelineSeries,
} from '../src/api/proxy-timeline';
import { el, select, disclosure } from './dom';
import { moneyAmount } from './home';

type Metric = 'cost' | 'tokens';
const palette = ['#446bc1', '#168578', '#b57629', '#9860aa', '#c6556c', '#557d37'];
function svg<K extends keyof SVGElementTagNameMap>(
  tag: K,
  attrs: Record<string, string | number>,
  text = '',
): SVGElementTagNameMap[K] {
  const node = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
  node.textContent = text;
  return node;
}
export function timelineView(
  initial: MoneyTimeline,
  load: (range: ChartRange, group: ChartGroup) => Promise<MoneyTimeline>,
): HTMLElement {
  let data = initial;
  let metric: Metric = 'cost';
  let group: ChartGroup = 'total';
  let range = initial.range;
  let currency = data.currencies[0] ?? 'USD';
  let selected = -1;
  let sequence = 0;
  let failed = false;
  const colourGroups: Partial<Record<ChartGroup, Map<string, string>>> = {};
  function colours(): Map<string, string> {
    return (colourGroups[group] ??= new Map());
  }
  function colour(key: string): string {
    const cache = colours();
    if (!cache.has(key)) {
      let hash = 0;
      for (const char of key) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
      let index = hash % palette.length;
      for (let n = 0; n < palette.length && [...cache.values()].includes(palette[index]!); n++)
        index = (index + 1) % palette.length;
      cache.set(key, palette[index]!);
    }
    return cache.get(key)!;
  }
  const view = el('div');
  view.className = 'timeline';
  const title = el('h2', 'Spending');
  title.tabIndex = -1;
  const total = el('p');
  total.className = 'timeline-total';
  const metricControl = select(
    [
      ['cost', 'Cost'],
      ['tokens', 'Tokens'],
    ],
    metric,
  );
  const groupControl = select(
    [
      ['total', 'Total'],
      ['client', 'By client'],
      ['provider', 'By provider'],
    ],
    group,
  );
  const rangeControl = select(
    [
      ['today', 'Today'],
      ['7d', '7 days'],
      ['30d', '30 days'],
    ],
    range,
  );
  const currencyControl = select([], currency);
  for (const [control, label] of [
    [metricControl, 'Metric'],
    [groupControl, 'Grouping'],
    [rangeControl, 'Range'],
    [currencyControl, 'Currency'],
  ] as const)
    control.setAttribute('aria-label', label);
  const controls = el('div', metricControl, groupControl, rangeControl, currencyControl);
  controls.className = 'timeline-controls';
  const heading = el('div', el('div', title, total), controls);
  heading.className = 'timeline-heading';
  const plot = el('div');
  plot.className = 'timeline-plot';
  const tooltip = el('div');
  tooltip.className = 'timeline-tooltip';
  tooltip.role = 'status';
  const legend = el('div');
  legend.className = 'timeline-legend';
  const details = disclosure(
    '',
    el(
      'p',
      'Only use that passed through this box is shown. Estimates are not bills. Missing amounts are excluded, not zero; totals may rise. Plans are not API-priced; fees, allowance and extras unknown.',
    ),
    el(
      'p',
      'In-flight use, retries and cancellations can add costs. Dates: Requests. No exchange rate is assumed. Tokens without usage stay unknown; local runs exclude electricity/hardware.',
    ),
    el(
      'p',
      'Budget is a guide, not a spending cap. It never stops requests; unpriced use and other currencies are not fully deducted.',
    ),
  );
  const status = details.querySelector('summary')!;
  view.append(heading, plot, legend, tooltip, details);
  function value(series: TimelineSeries): number | undefined {
    if (metric === 'tokens') return series.tokens;
    return series.costs.find((cost) => cost.currency === currency)?.micros;
  }
  function format(value: number): string {
    return metric === 'cost' ? moneyAmount(value, currency) : `${value.toLocaleString()} tokens`;
  }
  function inspect(index: number) {
    selected = index;
    for (const bar of plot.querySelectorAll<SVGGElement>('[data-bucket]')) {
      const active = Number(bar.dataset.bucket) === index;
      bar.classList.toggle('selected', active);
      bar.setAttribute('aria-pressed', String(active));
    }
    tooltip.replaceChildren();
    const bucket = data.buckets[index];
    if (!bucket) {
      tooltip.append(el('span', 'Select bar'));
      return;
    }
    tooltip.append(el('strong', bucket.label));
    const items = el('div');
    for (const series of bucket.series) {
      const amount = value(series);
      const unknown = metric === 'cost' ? series.unknown_cost : series.unknown_tokens;
      const estimated =
        metric === 'cost' && series.costs.some((c) => c.currency === currency && c.estimated);
      const text =
        amount === undefined
          ? metric === 'cost' && series.plan && !unknown
            ? 'Plan activity'
            : 'Not measured'
          : `${unknown ? '≥ ' : estimated ? '≈ ' : ''}${format(amount)}`;
      const row = el(
        'span',
        `${series.label} · ${text}${unknown ? ` · ${unknown} missing` : ''}${metric === 'cost' && series.plan ? ` · ${series.plan} plan` : ''}`,
      );
      items.append(row);
    }
    if (!bucket.series.length) items.append(el('span', 'No activity'));
    tooltip.append(items);
  }
  function draw() {
    if (failed) return;
    currencyControl.replaceChildren(
      ...data.currencies.map((code) => {
        const option = el('option', code);
        option.value = code;
        return option;
      }),
    );
    if (!data.currencies.includes(currency)) currency = data.currencies[0] ?? 'USD';
    currencyControl.value = currency;
    currencyControl.hidden = metric !== 'cost' || data.currencies.length < 2;
    title.textContent = metric === 'cost' ? 'Spending' : 'Tokens';
    const all = data.buckets.flatMap((bucket) => bucket.series);
    const active = new Map(all.map((series) => [series.key, series.label]));
    const cache = colours();
    for (const key of cache.keys()) if (!active.has(key)) cache.delete(key);
    const amounts = all.map(value).filter((v): v is number => v !== undefined);
    const unknown = all.reduce(
      (sum, series) => sum + (metric === 'cost' ? series.unknown_cost : series.unknown_tokens),
      0,
    );
    const estimate =
      metric === 'cost' &&
      all.some((series) => series.costs.some((c) => c.currency === currency && c.estimated));
    const sum = amounts.reduce((a, b) => a + b, 0);
    total.textContent = amounts.length
      ? `${unknown ? '≥ ' : estimate ? '≈ ' : ''}${format(sum)}`
      : all.length
        ? 'Not measured'
        : 'No activity';
    status.textContent = `${unknown ? `${unknown} missing · ` : ''}${metric === 'cost' && estimate ? 'Estimates' : data.timezone} · Details`;
    if (!Number.isSafeInteger(sum)) {
      total.textContent = 'Amount unavailable';
      plot.replaceChildren(el('p', 'Amount unavailable'));
      legend.replaceChildren();
      tooltip.replaceChildren();
      return;
    }
    const width = Math.max(320, plot.clientWidth || 960);
    const height = width < 600 ? 330 : 390;
    const left = width < 600 ? 56 : 72,
      right = 12,
      top = 20,
      bottom = height - 44;
    const chart = svg('svg', {
      viewBox: `0 0 ${width} ${height}`,
      role: 'group',
      'aria-label': `${metric === 'cost' ? currency + ' cost' : 'Tokens'} over time · ${data.timezone}`,
    });
    const sums = data.buckets.map((bucket) =>
      bucket.series.reduce((n, series) => n + (value(series) ?? 0), 0),
    );
    const maximum = Math.max(...sums, metric === 'cost' ? 10000 : 10) * 1.12;
    for (let tick = 0; tick <= 4; tick++) {
      const y = bottom - ((bottom - top) * tick) / 4;
      chart.append(
        svg('line', { x1: left, x2: width - right, y1: y, y2: y, class: 'timeline-grid' }),
      );
      const n = (maximum * tick) / 4;
      const label =
        metric === 'cost'
          ? moneyAmount(Math.round(n), currency)
          : Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(
              n,
            );
      chart.append(
        svg(
          'text',
          { x: left - 10, y: y + 4, 'text-anchor': 'end', class: 'timeline-axis' },
          label,
        ),
      );
    }
    const step = (width - left - right) / Math.max(1, data.buckets.length);
    data.buckets.forEach((bucket, index) => {
      const x = left + step * index;
      const bar = svg('g', {
        role: 'button',
        tabindex: 0,
        'data-bucket': index,
        'aria-label': bucket.label,
      });
      // A full-height hit target makes tiny bars and missing markers tappable.
      bar.append(
        svg('rect', { x, y: top, width: step, height: bottom - top + 8, class: 'timeline-hit' }),
      );
      let y = bottom;
      for (const series of bucket.series) {
        const amount = value(series);
        if (amount === undefined) continue;
        const h = ((bottom - top) * amount) / maximum;
        y -= h;
        bar.append(
          svg('rect', {
            x: x + step * 0.17,
            y,
            width: step * 0.66,
            height: Math.max(1, h),
            fill: group === 'total' ? palette[0]! : colour(series.key),
            rx: 2,
          }),
        );
      }
      const missing = bucket.series.some((series) =>
        metric === 'cost' ? series.unknown_cost > 0 : series.unknown_tokens > 0,
      );
      if (missing)
        bar.append(
          svg('circle', { cx: x + step / 2, cy: bottom + 7, r: 3, class: 'timeline-missing' }),
        );
      bar.addEventListener('pointerenter', () => inspect(index));
      bar.addEventListener('focus', () => inspect(index));
      bar.addEventListener('click', () => inspect(index));
      bar.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          inspect(index);
        }
        if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
          event.preventDefault();
          const next =
            (index + (event.key === 'ArrowRight' ? 1 : -1) + data.buckets.length) %
            data.buckets.length;
          plot.querySelector<SVGGElement>(`[data-bucket="${next}"]`)?.focus();
        }
      });
      chart.append(bar);
      const every = Math.max(1, Math.ceil(data.buckets.length / (width < 600 ? 3 : 6)));
      if (index % every === 0 || index === data.buckets.length - 1) {
        const date = new Date(bucket.start);
        const label = new Intl.DateTimeFormat(undefined, {
          timeZone: data.timezone,
          ...(range === 'today'
            ? { hour: 'numeric', timeZoneName: 'shortOffset' }
            : { day: 'numeric', month: 'short' }),
        }).format(date);
        chart.append(
          svg(
            'text',
            { x: x + step / 2, y: height - 14, 'text-anchor': 'middle', class: 'timeline-axis' },
            label,
          ),
        );
      }
    });
    plot.replaceChildren(chart);
    legend.replaceChildren();
    for (const [key, label] of active) {
      const swatch = el('i');
      swatch.style.backgroundColor = group === 'total' ? palette[0]! : colour(key);
      legend.append(el('span', swatch, label));
    }
    if (unknown) legend.append(el('span', '○ Missing'));
    inspect(selected);
  }
  async function reload() {
    const current = ++sequence;
    range = rangeControl.value as ChartRange;
    group = groupControl.value as ChartGroup;
    view.setAttribute('aria-busy', 'true');
    try {
      const next = await load(range, group);
      if (current !== sequence) return;
      data = next;
      failed = false;
      selected = -1;
      draw();
    } catch {
      if (current !== sequence) return;
      failed = true;
      plot.replaceChildren(el('p', 'Unavailable, not zero. Change range to retry.'));
      total.textContent = 'Unavailable';
      tooltip.replaceChildren();
      legend.replaceChildren();
    } finally {
      if (current === sequence) view.setAttribute('aria-busy', 'false');
    }
  }
  metricControl.addEventListener('change', () => {
    metric = metricControl.value as Metric;
    draw();
  });
  currencyControl.addEventListener('change', () => {
    currency = currencyControl.value;
    draw();
  });
  groupControl.addEventListener('change', () => {
    void reload();
  });
  rangeControl.addEventListener('change', () => {
    void reload();
  });
  draw();
  // Window resize avoids retaining detached views in a long-lived observer.
  const resize = () => {
    if (view.isConnected) draw();
    else window.removeEventListener('resize', resize);
  };
  window.addEventListener('resize', resize);
  queueMicrotask(() => {
    if (view.isConnected) draw();
  });
  return view;
}
