import { describe, expect, it } from 'vitest';
import type { ProxyObservedUsage, ProxyRequest } from '../src/api/proxy-client';
import {
  cacheValue,
  clientLabel,
  countValue,
  dateLabel,
  dayKey,
  durationValue,
  normalizedInput,
  priceValue,
  requestGroups,
  statusValue,
  timestamp,
  timeValue,
} from './requests-ledger';

function request(usage?: ProxyObservedUsage): ProxyRequest {
  return {
    id: 'r',
    seq: 1,
    started: new Date(2026, 10, 17, 14, 5, 9).getTime(),
    ended: 1,
    caller_id: 'caller',
    caller_name: 'Client',
    route: 'route',
    revision: 1,
    method: 'POST',
    path: '/v1/messages',
    peer: '',
    status: 200,
    outcome: 'complete',
    sent: 0,
    received: 0,
    header_ms: 0,
    first_byte_ms: 0,
    total_ms: 99,
    upstream_id: '',
    usage,
  };
}

describe('normalized request input and cache', () => {
  it.each(['messages', 'bedrock'] as const)(
    'sums all three disjoint %s input counters',
    (protocol) => {
      const row = request({ protocol, usage: { input: 2, cached_input: 40, cache_write: 58 } });
      expect(normalizedInput(row)).toBe(100);
      expect(cacheValue(row).text).toBe('40%');
      expect(cacheValue(row).title).toContain('40 / 100');
      delete row.usage!.usage!.cache_write;
      expect(normalizedInput(row)).toBeUndefined();
    },
  );
  it.each(['responses', 'chat_completions', 'gemini'] as const)(
    'never adds cached input again for %s',
    (protocol) => {
      const row = request({ protocol, usage: { input: 100, cached_input: 40, cache_write: 10 } });
      expect(normalizedInput(row)).toBe(100);
      expect(cacheValue(row).text).toBe('40%');
      delete row.usage!.usage!.cache_write;
      expect(normalizedInput(row)).toBe(100);
    },
  );
  it.each(['ollama', 'not_applicable', '', undefined] as const)(
    'does not assume overlap for %s',
    (protocol) => {
      expect(
        normalizedInput(
          request({ protocol, usage: { input: 100, cached_input: 0, cache_write: 0 } }),
        ),
      ).toBeUndefined();
    },
  );
  it.each([
    { input: -1, cached_input: 0, cache_write: 0 },
    { input: 1.2, cached_input: 0, cache_write: 0 },
    { input: NaN, cached_input: 0, cache_write: 0 },
    { input: Infinity, cached_input: 0, cache_write: 0 },
    { input: Number.MAX_SAFE_INTEGER, cached_input: 1, cache_write: 0 },
    { input: 100, cached_input: 0, cache_write: 0, ambiguous: true },
    { input: 100, cached_input: 0, cache_write: 0, source: 'unknown' as const },
    { input: 100, cached_input: 0, cache_write: 0, source: 'not_applicable' as const },
  ])('keeps unsafe or uncertain input unknown: %j', (usage) => {
    expect(normalizedInput(request({ protocol: 'messages', usage }))).toBeUndefined();
  });
  it('preserves zero and treats a zero denominator or missing reads as unknown percentages', () => {
    const row = request({
      protocol: 'messages',
      usage: { input: 0, cached_input: 0, cache_write: 0 },
    });
    expect(normalizedInput(row)).toBe(0);
    expect(countValue(normalizedInput(row), 'Input').text).toBe('0');
    expect(cacheValue(row).text).toBe('—');
    row.usage = { protocol: 'responses', usage: { input: 100 } };
    expect(cacheValue(row).text).toBe('—');
    row.usage.usage!.cached_input = 101;
    expect(cacheValue(row).text).toBe('—');
    row.usage.usage!.cached_input = 100;
    row.usage.usage!.cache_write = 1;
    expect(cacheValue(row).text).toBe('—');
  });
  it('does not round near-total cache reads to 100% when fresh input or writes exist', () => {
    const row = request({
      protocol: 'messages',
      usage: { input: 0, cached_input: 100000, cache_write: 1 },
    });
    expect(cacheValue(row).text).toBe('99.9%');
    row.usage!.usage!.cache_write = 0;
    expect(cacheValue(row).text).toBe('100%');
  });
  it('marks partial usage lower bounds, retaining unknown counters and avoiding a misleading ratio', () => {
    const row = request({
      protocol: 'messages',
      usage: { input: 5, cached_input: 10, cache_write: 0, source: 'partial' },
    });
    const input = countValue(normalizedInput(row), 'Total input', true);
    expect(input.text).toBe('≥15');
    expect(input.title).toContain('partial lower bound');
    expect(countValue(row.usage!.usage!.output, 'Output', true).text).toBe('—');
    expect(cacheValue(row)).toEqual({
      text: '—',
      title: 'Cache read share: unknown · partial usage',
    });
  });
  it('compacts large counts while preserving exact values in titles', () => {
    expect(countValue(36123, 'Input').text).toBe('36.1k');
    expect(countValue(36123, 'Input').title).toContain((36123).toLocaleString());
    for (const invalid of [undefined, -1, 1.2, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])
      expect(countValue(invalid, 'Input').text).toBe('—');
  });
});

describe('ledger price and durations', () => {
  it.each([
    [0, '0.0s'],
    [1, '<0.1s'],
    [99, '<0.1s'],
    [100, '0.1s'],
    [1299, '1.3s'],
    [60000, '60.0s'],
  ] as const)('formats %i ms as %s', (ms, expected) => {
    expect(durationValue(ms).text).toBe(expected);
    expect(durationValue(ms).title).toContain(ms.toLocaleString());
  });
  it.each([undefined, -1, Infinity, NaN])('keeps unknown duration unknown: %s', (ms) => {
    expect(durationValue(ms).text).toBe('—');
  });
  it('distinguishes charged, estimated, partial lower bounds and counterfactual plan prices', () => {
    const row = request();
    row.money = {
      billing: 'pay_as_you_go',
      kind: 'charged',
      amount_micros: 1200000,
      currency: 'USD',
    };
    expect(priceValue(row).text).toBe('$1.20');
    expect(priceValue(row).title).toContain('Actual charged');
    row.money.kind = 'estimated';
    expect(priceValue(row).text).toBe('≈$1.20');
    row.money.kind = 'estimated_floor';
    expect(priceValue(row).text).toBe('≥$1.20');
    row.money.billing = 'plan';
    row.money.api_equivalent = { kind: 'estimated', amount_micros: 600000, currency: 'USD' };
    expect(priceValue(row).text).toBe('≈$0.6000 API');
    expect(priceValue(row).title).toContain('not spend');
    row.money.api_equivalent.kind = 'estimated_floor';
    expect(priceValue(row).text).toBe('≥$0.6000 API');
    delete row.money.api_equivalent;
    expect(priceValue(row).text).toBe('—');
  });
  it('preserves true zeros and never rounds a positive price to zero', () => {
    const row = request();
    row.money = { billing: 'pay_as_you_go', kind: 'estimated', amount_micros: 0, currency: 'USD' };
    expect(priceValue(row).text).toBe('≈$0.00');
    row.money.amount_micros = 1;
    expect(priceValue(row).text).toBe('≈<$0.0001');
    expect(priceValue(row).title).toContain('USD 0.000001');
    row.money.kind = 'estimated_floor';
    expect(priceValue(row).text).toBe('≥$0.000001');
    row.money.amount_micros = 0;
    expect(priceValue(row).text).toBe('—');
    row.money.kind = 'estimated';
    row.money.amount_micros = 10;
    row.usage = { usage: { source: 'partial' } };
    expect(priceValue(row).text).toBe('≥$0.00001');
    row.money.kind = 'charged';
    expect(priceValue(row).text).toBe('<$0.0001');
    expect(priceValue(row).title).toContain('Actual charged');
  });
  it('keeps four decimal places below a dollar and never rounds a lower bound upward', () => {
    const row = request();
    row.money = {
      billing: 'pay_as_you_go',
      kind: 'estimated',
      amount_micros: 49900,
      currency: 'USD',
    };
    expect(priceValue(row).text).toBe('≈$0.0499');
    row.money.kind = 'estimated_floor';
    row.money.amount_micros = 4590;
    expect(priceValue(row).text).toBe('≥$0.0045');
    expect(priceValue(row).title).toContain('USD 0.00459');
    row.money.amount_micros = 1999999;
    expect(priceValue(row).text).toBe('≥$1.99');
    expect(countValue(1099, 'Input', true).text).toBe('≥1k');
  });
  it('labels local execution and pre-dispatch blocking rather than unknown pricing', () => {
    const row = request();
    row.money = { billing: 'local', kind: 'none' };
    expect(priceValue(row)).toEqual({
      text: 'local',
      title: 'Local execution · no provider charge',
    });
    row.money = { billing: 'pay_as_you_go', kind: 'no_charge_expected' };
    expect(priceValue(row)).toEqual({
      text: '—',
      title: 'Blocked before upstream dispatch · no charge expected',
    });
  });
  it('rejects missing, invalid, unsafe amounts and invalid currencies', () => {
    const row = request();
    expect(priceValue(row).text).toBe('—');
    for (const amount_micros of [undefined, -1, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      row.money = { billing: 'pay_as_you_go', kind: 'charged', amount_micros, currency: 'USD' };
      expect(priceValue(row).text).toBe('—');
    }
    row.money = { billing: 'pay_as_you_go', kind: 'charged', amount_micros: 10, currency: '<img>' };
    expect(priceValue(row).text).toBe('—');
  });
});

describe('local dates and completion status', () => {
  it('uses local HH:mm:ss for ISO, numeric and numeric-string timestamps', () => {
    const value = new Date(2026, 10, 17, 14, 5, 9);
    for (const raw of [value.getTime(), String(value.getTime()), value.toISOString()]) {
      expect(timeValue(raw).text).toBe('14:05:09');
      expect(timeValue(raw).title).toBe(value.toString());
      expect(dayKey(raw)).toBe('2026-10-17');
    }
    expect(timestamp(0)?.getTime()).toBe(0);
  });
  it.each(['', 'NaN', 'Infinity', 'not-a-date', '2026-99-99T00:00:00Z', Infinity, NaN, 1e30])(
    'keeps invalid dates safe: %s',
    (raw) => {
      expect(timestamp(raw)).toBeUndefined();
      expect(timeValue(raw).text).toBe('—');
      expect(dateLabel(raw)).toBe('Unknown date');
    },
  );
  it('groups calendar days locally, including yesterday across the year boundary', () => {
    const now = new Date(2026, 10, 17, 0, 1);
    expect(dateLabel(new Date(2026, 10, 17, 23).getTime(), now)).toBe('Today · Tue 17 Nov');
    expect(dateLabel(new Date(2026, 10, 16).getTime(), now)).toBe('Yesterday · Mon 16 Nov');
    expect(dateLabel(new Date(2025, 10, 17).getTime(), now)).toContain('2025');
    expect(dateLabel(new Date(2025, 11, 31).getTime(), new Date(2026, 0, 1))).toContain(
      'Yesterday · Wed 31 Dec 2025',
    );
  });
  it('does not equate HTTP 200 with completion', () => {
    const row = request();
    expect(statusValue(row).tone).toBe('complete');
    expect(statusValue(row).word).toBe('');
    for (const outcome of ['incomplete', 'canceled_or_timeout', 'transport_error']) {
      row.outcome = outcome;
      expect(statusValue(row).tone).toBe('warning');
      expect(statusValue(row).code).toBe('200');
      expect(statusValue(row).title).toContain('completion not confirmed');
      expect(statusValue(row).word).toBe('Interrupted');
    }
    for (const outcome of ['active', 'unknown', 'new_outcome']) {
      row.outcome = outcome;
      expect(statusValue(row).tone).toBe('unknown');
    }
    for (const status of [400, 429, 500]) {
      row.status = status;
      expect(statusValue(row).tone).toBe('error');
    }
    row.status = 0;
    row.outcome = 'complete';
    expect(statusValue(row).tone).toBe('unknown');
  });
});

describe('WebSocket connection status', () => {
  it.each([
    ['closed_by_client', 'Closed by client'],
    ['closed_by_upstream', 'Closed by upstream'],
    ['closed_by_shutdown', 'Closed on shutdown'],
    ['closed_by_revocation', 'Access revoked'],
    ['lifetime_exceeded', 'Lifetime limit reached'],
  ])('labels %s without implying response completion', (outcome, label) => {
    expect(statusValue({ ...request(), status: 101, outcome })).toEqual({
      text: `101 ${label}`,
      code: '101',
      word: label,
      tone: 'upgraded',
      title: `HTTP 101 · WebSocket upgrade succeeded · ${label}`,
    });
  });
  it('confirms only the upgrade when the connection is active or the outcome is unknown', () => {
    const row = { ...request(), status: 101 };
    expect(statusValue({ ...row, outcome: 'active' }).text).toBe('101 Active');
    for (const outcome of ['unknown', 'closed_by_client_extra', 'complete']) {
      expect(statusValue({ ...row, outcome })).toMatchObject({
        text: '101 Outcome unknown',
        title: 'HTTP 101 · WebSocket upgrade succeeded · Outcome unknown',
      });
    }
  });
  it('does not infer a WebSocket upgrade from a closure code alone', () => {
    const row = { ...request(), outcome: 'closed_by_revocation' };
    expect(statusValue(row)).toMatchObject({ text: '200 Access revoked', tone: 'warning' });
    expect(statusValue({ ...row, status: 503 })).toMatchObject({
      tone: 'error',
      title: 'HTTP 503 · Access revoked',
    });
  });
});

describe('consecutive unmeasured HTTP error grouping', () => {
  it('labels tagged clients and keeps the name and ID fallbacks', () => {
    expect(clientLabel(request())).toBe('Client');
    expect(clientLabel({ ...request(), tag: 'laptop' })).toBe('Client · laptop');
    expect(clientLabel({ ...request(), caller_name: '', tag: 'laptop' })).toBe('caller · laptop');
    expect(clientLabel({ ...request(), caller_name: '', caller_id: '' })).toBe('Unknown client');
  });

  const error = (): ProxyRequest => ({ ...request(), status: 429, outcome: 'http_error' });
  it('groups only consecutive equal errors and never across a local day', () => {
    const first = error();
    const other = { ...error(), route: 'other' };
    expect(requestGroups([first, error(), other, error()]).map((g) => g.length)).toEqual([2, 1, 1]);
    const nextDay = { ...error(), started: new Date(2026, 10, 18).getTime() };
    expect(requestGroups([first, nextDay])).toHaveLength(2);
    expect(
      requestGroups([
        { ...first, started: 'bad' },
        { ...first, started: 'bad' },
      ]),
    ).toHaveLength(2);
  });
  it.each([
    { method: 'GET' },
    { path: '/other' },
    { local_prefix: '/other' },
    { route: 'other' },
    { caller_id: 'other' },
    { revision: 2 },
    { caller_name: 'Other' },
    { tag: 'laptop' },
    { status: 500 },
    { outcome: 'incomplete' },
    { usage: { usage: { model: 'other' } } },
  ])('does not hide a distinct error: %j', (change) => {
    expect(requestGroups([error(), { ...error(), ...change }])).toHaveLength(2);
  });
  it.each(['input', 'output', 'cached_input', 'cache_write', 'reasoning', 'total'] as const)(
    'never groups measured %s, even zero',
    (counter) => {
      const row = error();
      row.usage = { usage: { [counter]: 0, source: 'partial' } };
      expect(requestGroups([row, row])).toHaveLength(2);
    },
  );
  it.each(['charged', 'estimated', 'estimated_floor', 'included'] as const)(
    'never groups %s requests',
    (kind) => {
      const row = error();
      row.money = { billing: 'pay_as_you_go', kind };
      expect(requestGroups([row, row])).toHaveLength(2);
    },
  );
  it('preserves plan equivalence and even zero price evidence', () => {
    const row = error();
    row.money = {
      billing: 'plan',
      kind: 'unmeasured',
      api_equivalent: { kind: 'estimated', amount_micros: 0 },
    };
    expect(requestGroups([row, row])).toHaveLength(2);
    row.money = { billing: 'pay_as_you_go', kind: 'unpriced', amount_micros: 0 };
    expect(requestGroups([row, row])).toHaveLength(2);
  });
});

it('does not group unmeasured errors for different requested models', () => {
  const error = { ...request(), status: 400, outcome: 'http_error', usage: undefined };
  const a = { ...error, request_meta: { state: 'observed' as const, model: 'model-a' } };
  const b = { ...error, request_meta: { state: 'observed' as const, model: 'model-b' } };
  expect(requestGroups([a, b, b]).map((group) => group.length)).toEqual([1, 2]);
});
