import { describe, expect, it } from 'vitest';
import type { ProxyRequest } from '../src/api/proxy-client';
import { effortValue, finishValue, thinkingValue } from './request-insights';

function row(overrides: Partial<ProxyRequest> = {}): ProxyRequest {
  return {
    id: 'r',
    seq: 1,
    started: 0,
    ended: 1,
    caller_id: 'c',
    caller_name: 'Client',
    route: 'test',
    revision: 1,
    method: 'POST',
    path: '/responses',
    peer: '',
    status: 200,
    outcome: 'complete',
    sent: 0,
    received: 0,
    header_ms: 0,
    first_byte_ms: 0,
    total_ms: 1,
    upstream_id: '',
    ...overrides,
  };
}

describe('summary thinking and effort', () => {
  it.each([undefined, -1, 0, NaN, Infinity, 0.5, Number.MAX_SAFE_INTEGER + 1])(
    'hides missing, invalid and zero thinking: %s',
    (reasoning) => {
      expect(
        thinkingValue(
          row({ usage: { protocol: 'responses', usage: { reasoning, source: 'reported' } } }),
        ),
      ).toBeUndefined();
    },
  );
  it.each(['responses', 'chat_completions', 'gemini'] as const)(
    'shows a positive %s split without a plus',
    (protocol) => {
      const value = thinkingValue(
        row({ usage: { protocol, usage: { reasoning: 5100, source: 'reported' } } }),
      );
      expect(value?.text).toBe('5.1k think');
      expect(value?.text).not.toContain('+');
      expect(value?.title).toContain(protocol === 'gemini' ? 'Additional' : 'included');
    },
  );
  it('preserves a partial lower bound and suppresses untrusted evidence', () => {
    expect(
      thinkingValue(
        row({ usage: { protocol: 'responses', usage: { reasoning: 5199, source: 'partial' } } }),
      )?.text,
    ).toBe('≥5.1k think');
    for (const source of ['unknown', 'not_applicable'] as const)
      expect(
        thinkingValue(
          row({ usage: { protocol: 'responses', usage: { reasoning: 5100, source } } }),
        ),
      ).toBeUndefined();
    expect(
      thinkingValue(
        row({
          usage: {
            protocol: 'responses',
            usage: { reasoning: 5100, source: 'reported', ambiguous: true },
          },
        }),
      ),
    ).toBeUndefined();
  });
  it('uses effort values alone and preserves explicit zero budgets', () => {
    expect(
      effortValue(row({ request_meta: { state: 'observed', reasoning_effort: 'HIGH' } })),
    ).toBe('high');
    expect(effortValue(row({ request_meta: { state: 'observed', thinking_budget: 0 } }))).toBe(
      '0 budget',
    );
    expect(effortValue(row())).toBe('');
  });
});

describe('termination summary is evidence, not generic success', () => {
  it.each([
    ['completed', 'Complete'],
    ['stop', 'Complete'],
    ['end_turn', 'Complete'],
    ['STOP', 'Complete'],
    ['length', 'Output limit'],
    ['MAX_TOKENS', 'Output limit'],
    ['pause_turn', 'Turn paused'],
    ['model_context_window_exceeded', 'Context limit'],
    ['RECITATION', 'Recitation'],
    ['MALFORMED_FUNCTION_CALL', 'Malformed tool call'],
    ['UNEXPECTED_TOOL_CALL', 'Unexpected tool call'],
    ['SAFETY', 'Safety'],
    ['IMAGE_SAFETY', 'image safety'],
    ['OTHER', 'other'],
    ['queued', 'queued'],
    ['failed', 'failed'],
  ])('%s is accurately labeled %s', (finish, expected) => {
    expect(finishValue(row({ response_meta: { state: 'observed', finish } }))).toBe(expected);
  });
  it('qualifies partial and interrupted/error outcomes regardless of provider finish', () => {
    const meta = { state: 'observed' as const, finish: 'stop' };
    expect(finishValue(row({ status: 500, response_meta: meta }))).toBe('Upstream error');
    for (const outcome of ['incomplete', 'transport_error', 'canceled_or_timeout'])
      expect(finishValue(row({ outcome, response_meta: meta }))).toBe('Interrupted');
    expect(finishValue(row({ response_meta: { state: 'partial', finish: 'length' } }))).toBe(
      'Partial',
    );
  });
});

describe('reviewed finish provenance', () => {
  it.each([
    ['denied', 403, 'Denied'],
    ['not_dispatched', 502, 'Not dispatched'],
    ['upstream_auth', 502, 'Auth error'],
    ['transport_error', 502, 'Interrupted'],
    ['http_error', 429, 'Upstream error'],
  ])('labels %s without inventing upstream dispatch', (outcome, status, expected) => {
    expect(finishValue(row({ outcome, status }))).toBe(expected);
  });
  it.each([
    [{ status: 'incomplete', incomplete_reason: 'max_output_tokens' }, 'Output limit'],
    [{ status: 'failed' }, 'failed'],
    [{ status: 'cancelled' }, 'cancelled'],
    [{ status: 'canceled' }, 'canceled'],
    [{ finish: 'content_filter' }, 'Filtered'],
  ])("uses this turn's correlated terminal: %j", (metadata, expected) => {
    const request = row({
      status: 101,
      outcome: 'incomplete',
      websocket_connection_id: 'connection',
      response_meta: { state: 'observed', ...metadata },
    });
    expect(finishValue(request)).toBe(expected);
    expect(finishValue({ ...request, outcome: 'transport_error' })).toBe('Interrupted');
    expect(
      finishValue({ ...request, response_meta: { ...request.response_meta!, state: 'partial' } }),
    ).toBe('Interrupted');
  });
  it('does not upgrade ambiguous, unknown or severed turns to success', () => {
    const request = row({
      status: 101,
      outcome: 'incomplete',
      websocket_connection_id: 'connection',
      response_meta: { state: 'observed', status: 'completed' },
    });
    expect(finishValue(request)).toBe('Interrupted');
    expect(finishValue({ ...request, outcome: 'unknown' })).toBe('Unknown');
    expect(
      finishValue({ ...request, outcome: 'complete', usage: { usage: { ambiguous: true } } }),
    ).toBe('Partial');
  });
});
