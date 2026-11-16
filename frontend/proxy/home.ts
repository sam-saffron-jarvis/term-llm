import type { ProxyClient, ProxyMoney, ProxyState } from '../src/api/proxy-client';
import { moneySettingsView } from './money-settings';
import { button, card, el, type UIContext } from './dom';
import { timelineView } from './timeline';

// Format only. Classification, source selection, aggregation and budgets belong
// to the backend; a missing amount must never be formatted as a zero.
export function moneyAmount(micros: number, currency: string): string {
  if (!Number.isSafeInteger(micros) || !/^[A-Z]{3}$/.test(currency)) return 'Amount unavailable';
  const value = micros / 1_000_000;
  const prefixes: Record<string, string> = {
    USD: 'US$',
    AUD: 'A$',
    CAD: 'CA$',
    NZD: 'NZ$',
    EUR: '€',
    GBP: '£',
  };
  return `${prefixes[currency] ?? currency + ' '}${value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: value !== 0 && Math.abs(value) < 0.01 ? 6 : 2,
  })}`;
}
export function moneyLabel(money?: ProxyMoney): string {
  if (!money) return 'Not measured';
  if (money.kind === 'no_charge_expected') return 'Blocked before dispatch · no charge expected';
  if (money.billing === 'plan')
    return money.kind === 'included'
      ? 'Measured plan activity · extra charges not reported'
      : 'Plan activity not fully measured · extra charges not reported';
  if (money.billing === 'local') return 'No provider charge';
  if (money.billing === 'unspecified') return 'Billing not specified';
  if (['charged', 'estimated', 'estimated_floor'].includes(money.kind)) {
    if (money.amount_micros === undefined || !money.currency) return 'Amount unavailable';
    const amount = moneyAmount(money.amount_micros, money.currency);
    if (money.kind === 'estimated') return `About ${amount}`;
    if (money.kind === 'estimated_floor') return `At least ${amount}`;
    return `${amount} · charged`;
  }
  const labels: Partial<Record<ProxyMoney['kind'], string>> = {
    unpriced: 'No price yet',
    unmeasured: "Couldn't measure",
    no_charge_expected: 'No charge expected',
  };
  return labels[money.kind] ?? 'Not measured';
}
export function providerLabel(name: string, state?: ProxyState): string {
  const route = state?.routes.find((route) => route.name === name);
  const kind = route?.account && state?.accounts?.find((a) => a.name === route.account)?.kind;
  const labels: Record<string, string> = {
    chatgpt: 'ChatGPT',
    'claude-code': 'Claude',
    'claude-bin': 'Claude',
    anthropic: 'Claude',
    'grok-bin': 'Grok',
    grok: 'Grok',
    xai: 'Grok',
    openai: 'OpenAI',
    copilot: 'GitHub Copilot',
    ollama: 'Ollama',
    unknown: 'Not identified',
  };
  const preset = state?.presets?.find((preset) => preset.upstream === route?.upstream);
  return (kind && labels[kind]) || labels[name] || (preset && labels[preset.id]) || name;
}
export function homeView(state: ProxyState, api: ProxyClient, context: UIContext): HTMLElement {
  const view = el('div', el('p', 'Loading chart…'));
  async function load() {
    view.setAttribute('aria-busy', 'true');
    const ok = await context.run(async () => {
      const timeline = await api.timeline('30d', 'total');
      if (!view.isConnected) return;
      view.replaceChildren(
        timelineView(timeline, (range, group) => api.timeline(range, group)),
        moneySettingsView(state, api, context),
      );
    });
    if (!view.isConnected) return;
    view.setAttribute('aria-busy', 'false');
    if (!ok)
      view.replaceChildren(
        card(
          'Chart unavailable',
          el('p', 'Not zero.'),
          button('Retry', () => {
            void load();
          }),
        ),
      );
  }
  queueMicrotask(() => {
    if (view.isConnected) void load();
  });
  return view;
}
