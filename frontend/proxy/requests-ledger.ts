import type { ProxyRequest } from '../src/api/proxy-client';

export const connectionOutcomes: [string, string][] = [
  ['closed_by_client', 'Closed by client'],
  ['closed_by_upstream', 'Closed by upstream'],
  ['closed_by_shutdown', 'Closed on shutdown'],
  ['closed_by_revocation', 'Access revoked'],
  ['lifetime_exceeded', 'Lifetime limit reached'],
];
export const webSocketUpgrade = 'HTTP 101 · WebSocket upgrade succeeded';

export interface LedgerValue {
  text: string;
  title: string;
}
const unknown = (title: string): LedgerValue => ({ text: '—', title });
export function knownCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}
export function isPartial(request: ProxyRequest): boolean {
  return request.usage?.usage?.source === 'partial';
}
export function normalizedInput(request: ProxyRequest): number | undefined {
  const usage = request.usage?.usage;
  if (!usage || usage.ambiguous || ['unknown', 'not_applicable'].includes(usage.source || ''))
    return undefined;
  const { input, cached_input, cache_write } = usage;
  if (!knownCount(input)) return undefined;
  switch (request.usage?.protocol) {
    case 'messages':
    case 'bedrock': {
      if (!knownCount(cached_input) || !knownCount(cache_write)) return undefined;
      const total = input + cached_input + cache_write;
      return knownCount(total) ? total : undefined;
    }
    case 'responses':
    case 'chat_completions':
    case 'gemini':
      return input;
    default:
      return undefined;
  }
}
export function countValue(value: unknown, label: string, partial = false): LedgerValue {
  if (!knownCount(value)) return unknown(`${label}: not reported`);
  // A compact partial lower bound must not round up beyond the observed count.
  const step = 10 ** Math.max(0, Math.floor(Math.log10(value) / 3) * 3 - 1);
  const compact = partial ? Math.floor(value / step) * step : value;
  const text =
    value < 1000
      ? String(value)
      : new Intl.NumberFormat('en-US', {
          notation: 'compact',
          maximumFractionDigits: 1,
        })
          .format(compact)
          .toLowerCase();
  return {
    text: `${partial ? '≥' : ''}${text}`,
    title: `${label}: ${value.toLocaleString()}${partial ? ' · partial lower bound' : ''}`,
  };
}
export function cacheValue(request: ProxyRequest): LedgerValue {
  const total = normalizedInput(request);
  const read = request.usage?.usage?.cached_input;
  const write = request.usage?.usage?.cache_write;
  if (
    total === undefined ||
    !knownCount(read) ||
    read > total ||
    (knownCount(write) && write > total - read) ||
    isPartial(request)
  )
    return unknown('Cache read share: unknown' + (isPartial(request) ? ' · partial usage' : ''));
  if (!total) return unknown('Cache read share: no input tokens (0 / 0)');
  // Do not round a cache hit with fresh input or writes up to 100%.
  const percent = read === total ? 100 : Math.min(99.9, Math.round((read / total) * 1000) / 10);
  return {
    text: `${percent}%`,
    title: `Cache reads: ${read.toLocaleString()} / ${total.toLocaleString()} total input tokens (${(read / total) * 100}%)`,
  };
}
export function durationValue(ms: unknown): LedgerValue {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0)
    return unknown('Duration: not reported');
  return {
    text: ms > 0 && ms < 100 ? '<0.1s' : `${(ms / 1000).toFixed(1)}s`,
    title: `Duration: ${ms.toLocaleString()} ms`,
  };
}
export function priceValue(request: ProxyRequest): LedgerValue {
  if (request.money?.kind === 'no_charge_expected')
    return unknown('Blocked before upstream dispatch · no charge expected');
  if (request.money?.billing === 'local')
    return { text: 'local', title: 'Local execution · no provider charge' };
  const plan = request.money?.billing === 'plan';
  const money = plan ? request.money?.api_equivalent : request.money;
  const amount = money?.amount_micros;
  const kind = money?.kind;
  if (
    !money ||
    !['charged', 'estimated', 'estimated_floor'].includes(kind || '') ||
    !knownCount(amount) ||
    !/^[A-Z]{3}$/.test(money.currency || '')
  )
    return unknown(
      plan
        ? 'API-equivalent estimate unavailable · plan billing, not spend'
        : 'Price unknown; not zero',
    );
  const floor = kind === 'estimated_floor' || (kind !== 'charged' && isPartial(request));
  if (floor && amount === 0) return unknown('Partial price unknown; no positive lower bound');
  const currency = money.currency === 'USD' ? '$' : `${money.currency} `;
  const decimals = amount > 0 && amount < 1e6 ? 4 : 2;
  const step = 10 ** (6 - decimals);
  const displayed = floor ? Math.floor(amount / step) * step : amount;
  const value =
    amount > 0 && amount < 100
      ? floor
        ? `${currency}${amount / 1e6}`
        : `<${currency}0.0001`
      : `${currency}${(displayed / 1e6).toFixed(decimals)}`;
  const prefix = floor ? '≥' : kind === 'charged' ? '' : '≈';
  return {
    text: `${prefix}${value}${plan ? ' API' : ''}`,
    title: `${plan ? 'Plan API-equivalent estimate, not spend' : kind === 'charged' ? 'Actual charged' : 'Estimated API cost'}: ${money.currency} ${amount / 1e6}${floor ? ' · partial lower bound' : ''}`,
  };
}
export function statusValue(
  request: ProxyRequest,
): LedgerValue & { code: string; word: string; tone: string } {
  if (request.websocket_connection_id) {
    const complete = request.outcome === 'complete';
    const incomplete = request.outcome === 'incomplete';
    const word = complete ? 'Turn complete' : incomplete ? 'Turn incomplete' : 'Outcome unknown';
    return {
      text: word,
      code: '',
      word,
      tone: complete ? 'complete' : incomplete ? 'warning' : 'unknown',
      title: `${word} · Transport: ${webSocketUpgrade}`,
    };
  }
  const code = request.status > 0 ? String(request.status) : '—';
  const closure = connectionOutcomes.find(([key]) => key === request.outcome)?.[1];
  const active = request.outcome === 'active';
  const interrupted = ['incomplete', 'canceled_or_timeout', 'transport_error'].includes(
    request.outcome,
  );
  if (request.status === 101) {
    const word = closure ?? (active ? 'Active' : interrupted ? 'Interrupted' : 'Outcome unknown');
    return {
      text: `${code} ${word}`,
      code,
      word,
      tone: 'upgraded',
      title: `${webSocketUpgrade} · ${word}`,
    };
  }
  if (request.status >= 400)
    return {
      text: code,
      code,
      word: 'Error',
      tone: 'error',
      title: `HTTP ${code} · ${closure ?? request.outcome}`,
    };
  if (closure)
    return {
      text: `${code} ${closure}`,
      code,
      word: closure,
      tone: 'warning',
      title: `HTTP ${code} · ${closure}`,
    };
  if (request.outcome === 'complete' && request.status >= 200 && request.status < 300)
    return { text: code, code, word: '', tone: 'complete', title: `HTTP ${code} · complete` };
  const word = active ? 'Active' : interrupted ? 'Interrupted' : 'Unknown';
  return {
    text: `${code} ${word}`,
    code,
    word,
    tone: interrupted ? 'warning' : 'unknown',
    title: `HTTP ${code} · ${request.outcome} · ${word.toLowerCase()}; completion not confirmed`,
  };
}
export function timestamp(value: string | number): Date | undefined {
  const raw =
    typeof value === 'string' && /^-?\d+(\.\d+)?$/.test(value.trim()) ? Number(value) : value;
  if (typeof raw === 'string' && !/^\d{4}-\d{2}-\d{2}(T|$)/.test(raw)) return undefined;
  const date = new Date(raw);
  return Number.isFinite(date.getTime()) ? date : undefined;
}
export function dayKey(value: string | number): string {
  const date = timestamp(value);
  return date ? `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}` : 'unknown';
}
export function dateLabel(value: string | number, now = new Date()): string {
  const date = timestamp(value);
  if (!date) return 'Unknown date';
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const prefix =
    dayKey(value) === dayKey(now.getTime())
      ? 'Today · '
      : dayKey(value) === dayKey(yesterday.getTime())
        ? 'Yesterday · '
        : '';
  return (
    prefix +
    date
      .toLocaleDateString('en-GB', {
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        ...(date.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
      })
      .replace(/,/g, '')
  );
}
export function timeValue(value: string | number): LedgerValue {
  const date = timestamp(value);
  return date
    ? {
        text: [date.getHours(), date.getMinutes(), date.getSeconds()]
          .map((n) => String(n).padStart(2, '0'))
          .join(':'),
        title: date.toString(),
      }
    : unknown('Start time unknown');
}
function unmeasuredError(request: ProxyRequest): boolean {
  if (request.status < 400 || request.outcome !== 'http_error') return false;
  const usage = request.usage?.usage;
  if (
    usage &&
    [
      usage.input,
      usage.output,
      usage.cached_input,
      usage.cache_write,
      usage.reasoning,
      usage.total,
    ].some(knownCount)
  )
    return false;
  return [request.money, request.money?.api_equivalent].every(
    (money) =>
      !money ||
      (!knownCount(money.amount_micros) &&
        ['unpriced', 'unmeasured', 'unspecified'].includes(money.kind)),
  );
}
export function clientLabel(
  request: Pick<ProxyRequest, 'caller_name' | 'caller_id' | 'tag'>,
): string {
  const name = request.caller_name || request.caller_id || 'Unknown client';
  return request.tag ? `${name} · ${request.tag}` : name;
}

export function requestGroups(requests: ProxyRequest[]): ProxyRequest[][] {
  const groups: ProxyRequest[][] = [];
  let previous = '';
  for (const request of requests) {
    const key =
      unmeasuredError(request) && dayKey(request.started) !== 'unknown'
        ? JSON.stringify([
            dayKey(request.started),
            request.method,
            request.local_prefix,
            request.path,
            request.route,
            request.revision,
            request.caller_id,
            request.caller_name,
            request.tag || '',
            request.status,
            request.outcome,
            request.usage?.usage?.model,
            request.request_meta?.model || '',
          ])
        : '';
    if (key && key === previous) groups.at(-1)!.push(request);
    else groups.push([request]);
    previous = key;
  }
  return groups;
}
