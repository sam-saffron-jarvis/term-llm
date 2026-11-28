import type { ProxyRequest } from '../src/api/proxy-client';
import { countValue, isPartial, knownCount, type LedgerValue } from './requests-ledger';

/** Only positive, usable reasoning evidence earns a collapsed summary line. */
export function thinkingValue(request: ProxyRequest): LedgerValue | undefined {
  const usage = request.usage?.usage;
  if (
    !usage ||
    !['responses', 'chat_completions', 'gemini'].includes(request.usage?.protocol || '') ||
    usage.ambiguous ||
    !['reported', 'partial'].includes(usage.source || '') ||
    !knownCount(usage.reasoning) ||
    usage.reasoning <= 0
  )
    return undefined;
  const value = countValue(usage.reasoning, 'Thinking tokens', isPartial(request));
  return {
    text: `${value.text} think`,
    title: `${value.title} · ${
      request.usage?.protocol === 'gemini'
        ? 'Additional to candidate output'
        : 'Already included in output'
    }`,
  };
}
export function effortValue(request: ProxyRequest): string {
  const meta = request.request_meta;
  if (!meta) return '';
  if (meta.reasoning_effort) return meta.reasoning_effort.toLowerCase();
  if (knownCount(meta.thinking_budget))
    return `${countValue(meta.thinking_budget, 'Thinking budget').text} budget`;
  if (meta.thinking_budget_mode) return meta.thinking_budget_mode;
  if (['enabled', 'on', 'adaptive'].includes(meta.thinking_mode || ''))
    return meta.thinking_mode === 'adaptive' ? 'adaptive' : 'thinking on';
  return '';
}
export function finishValue(request: ProxyRequest): string {
  if (request.outcome === 'denied') return 'Denied';
  if (request.outcome === 'not_dispatched') return 'Not dispatched';
  if (request.outcome === 'upstream_auth') return 'Auth error';
  if (['canceled_or_timeout', 'transport_error'].includes(request.outcome)) return 'Interrupted';
  if (request.status >= 400) return 'Upstream error';
  const meta = request.response_meta;
  if (request.outcome === 'incomplete') {
    const turnTerminal =
      request.websocket_connection_id &&
      meta?.state === 'observed' &&
      !request.usage?.usage?.ambiguous &&
      (meta.incomplete_reason ||
        meta.finish ||
        ['incomplete', 'failed', 'cancelled', 'canceled'].includes(meta.status || ''));
    if (!turnTerminal) return 'Interrupted';
  }
  if (!meta) return '';
  if (meta.state === 'partial' || request.usage?.usage?.ambiguous) return 'Partial';
  const reason = meta.incomplete_reason || meta.finish || meta.status;
  const labels: Record<string, string> = {
    max_output_tokens: 'Output limit',
    length: 'Output limit',
    max_tokens: 'Output limit',
    MAX_TOKENS: 'Output limit',
    tool_use: 'Tool call',
    tool_calls: 'Tool call',
    function_call: 'Tool call',
    content_filter: 'Filtered',
    SAFETY: 'Safety',
    refusal: 'Refusal',
    PROHIBITED_CONTENT: 'Prohibited content',
    BLOCKLIST: 'Blocked',
    completed: 'Complete',
    stop: 'Complete',
    end_turn: 'Complete',
    STOP: 'Complete',
    mixed: 'Mixed finish',
    pause_turn: 'Turn paused',
    model_context_window_exceeded: 'Context limit',
    RECITATION: 'Recitation',
    MALFORMED_FUNCTION_CALL: 'Malformed tool call',
    UNEXPECTED_TOOL_CALL: 'Unexpected tool call',
    TOO_MANY_TOOL_CALLS: 'Tool call limit',
    SPII: 'Sensitive information',
    LANGUAGE: 'Language',
  };
  const label = reason ? labels[reason] || reason.toLowerCase().replaceAll('_', ' ') : '';
  // An incomplete/unknown turn cannot be upgraded by a contradictory success.
  if (request.websocket_connection_id && request.outcome !== 'complete' && label === 'Complete')
    return request.outcome === 'incomplete' ? 'Interrupted' : 'Unknown';
  return label;
}
