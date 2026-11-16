import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, getByLabelText, getByRole, waitFor } from '@testing-library/preact';
import {
  ProxyClient,
  type ProxyMoneyRate,
  type ProxyMoneySettings,
  type ProxyState,
} from '../src/api/proxy-client';
import { budgetMicros, moneySettingsView, parseMoneyRates } from './money-settings';
import type { UIContext } from './dom';

const rate: ProxyMoneyRate = {
  provider: 'fixture',
  model: 'exact',
  tier: '',
  currency: 'USD',
  source: 'operator fixture',
  asof: '2026-11-16',
  input_micros_per_million: 2000000,
  output_micros_per_million: 6000000,
};
function setup(mode: 'ui' | 'file' = 'ui', settings: ProxyMoneySettings = { timezone: 'UTC' }) {
  const api = new ProxyClient(vi.fn());
  const get = vi.spyOn(api, 'moneySettings').mockResolvedValue(settings);
  const save = vi.spyOn(api, 'money').mockResolvedValue();
  const announce = vi.fn();
  const refresh = vi.fn();
  const context: UIContext = {
    run: async (action) => {
      try {
        await action();
        return true;
      } catch (error) {
        announce(String(error));
        return false;
      }
    },
    announce,
    refresh,
    navigate: vi.fn(),
    protectToken: vi.fn(),
  };
  const view = moneySettingsView({ mode } as ProxyState, api, context) as HTMLDetailsElement;
  document.body.append(view);
  async function open() {
    view.open = true;
    view.dispatchEvent(new Event('toggle'));
    await waitFor(() => expect(getByLabelText(view, 'Spending timezone')).toBeTruthy());
  }
  return { api, get, save, announce, refresh, view, open };
}
afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});
describe('money settings', () => {
  it('loads real bare settings only when opened and saves exact budget plus dated cards', async () => {
    const { view, get, save, refresh, open } = setup();
    expect(get).not.toHaveBeenCalled();
    await open();
    fireEvent.input(getByLabelText(view, 'Spending timezone'), {
      target: { value: 'Australia/Sydney' },
    });
    fireEvent.input(getByLabelText(view, 'Budget currency'), { target: { value: 'aud' } });
    fireEvent.input(getByLabelText(view, 'Monthly warning budget'), {
      target: { value: '40.123456' },
    });
    fireEvent.input(getByLabelText(view, 'Price cards JSON'), {
      target: { value: JSON.stringify([rate]) },
    });
    fireEvent.click(getByRole(view, 'button', { name: 'Save money settings' }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith({
        timezone: 'Australia/Sydney',
        budget: { currency: 'AUD', limit_micros: 40123456 },
        rates: [rate],
      }),
    );
    expect(refresh).toHaveBeenCalledOnce();
    expect(view.textContent).toContain('Budget is a guide, not a spending cap');
    expect(view.textContent).toContain('plan extras can add costs');
  });
  it('does not erase configured rates when only the budget changes', async () => {
    const { view, save, open } = setup('ui', {
      timezone: 'UTC',
      budget: { currency: 'USD', limit_micros: 1000001 },
      rates: [rate],
    });
    await open();
    expect((getByLabelText(view, 'Monthly warning budget') as HTMLInputElement).value).toBe(
      '1.000001',
    );
    fireEvent.input(getByLabelText(view, 'Monthly warning budget'), { target: { value: '' } });
    fireEvent.click(getByRole(view, 'button', { name: 'Save money settings' }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith({ timezone: 'UTC', budget: undefined, rates: [rate] }),
    );
  });
  it('shows file-managed settings read-only and makes no PUT', async () => {
    const { view, save, open } = setup('file', { timezone: 'UTC', rates: [rate] });
    await open();
    expect(view.textContent).toContain('file-managed and read-only');
    for (const control of view.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(
      'input,textarea',
    ))
      expect(control.readOnly).toBe(true);
    expect(view.querySelector('button[type="submit"]')).toBeNull();
    expect(save).not.toHaveBeenCalled();
  });
  it('offers retry when GET fails, without inventing empty settings', async () => {
    const { view, get, open } = setup();
    get.mockRejectedValueOnce(new Error('unavailable'));
    view.open = true;
    view.dispatchEvent(new Event('toggle'));
    await waitFor(() =>
      expect(getByRole(view, 'button', { name: 'Retry money settings' })).toBeTruthy(),
    );
    expect(view.querySelector('input')).toBeNull();
    fireEvent.click(getByRole(view, 'button', { name: 'Retry money settings' }));
    await open();
    expect(get).toHaveBeenCalledTimes(2);
  });
  it('does not PUT incomplete prices or invented zero rates', async () => {
    const { view, save, open } = setup();
    await open();
    const incomplete: Partial<ProxyMoneyRate> = { ...rate };
    delete incomplete.input_micros_per_million;
    fireEvent.input(getByLabelText(view, 'Price cards JSON'), {
      target: { value: JSON.stringify([incomplete]) },
    });
    fireEvent.click(getByRole(view, 'button', { name: 'Save money settings' }));
    expect(save).not.toHaveBeenCalled();
  });
  it('uses bare GET money settings with authenticated existing transport', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(JSON.stringify({ timezone: 'UTC', rates: [rate] })));
    expect(await new ProxyClient(fetcher).moneySettings()).toEqual({
      timezone: 'UTC',
      rates: [rate],
    });
    expect(fetcher).toHaveBeenCalledWith(
      '/_proxy/api/money',
      expect.objectContaining({ method: 'GET', credentials: 'same-origin', cache: 'no-store' }),
    );
  });
});
it('parses decimal budgets without floating-point money rounding', () => {
  expect(budgetMicros('0.000001')).toBe(1);
  expect(budgetMicros('10.01')).toBe(10010000);
  expect(budgetMicros('')).toBeUndefined();
  for (const value of ['-1', '1.0000001', '1e3', 'NaN', '9007199254.740992'])
    expect(() => budgetMicros(value)).toThrow();
});
it('validates exact dated card semantics while preserving explicit zero cache prices', () => {
  expect(
    parseMoneyRates(JSON.stringify([{ ...rate, cache_read_micros_per_million: 0 }]))[0]
      ?.cache_read_micros_per_million,
  ).toBe(0);
  for (const invalid of [
    [rate, rate],
    [{ ...rate, asof: '2026-02-30' }],
    [{ ...rate, input_micros_per_million: -1 }],
    [{ ...rate, input_micros_per_million: null }],
    [{ ...rate, output_micros_per_million: 0.1 }],
    [{ ...rate, fuzzy_model: true }],
  ])
    expect(() => parseMoneyRates(JSON.stringify(invalid))).toThrow();
});
