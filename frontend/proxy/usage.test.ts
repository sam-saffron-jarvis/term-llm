import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByRole,
  getByText,
  queryByRole,
  waitFor,
  within,
} from '@testing-library/preact';
import { ProxyClient, type ProxyState, type ProxyUsageSnapshot } from '../src/api/proxy-client';
import { mountProxy } from './app';

const state: ProxyState = {
  defaults: null,
  providers: [],
  auth: 'token',
  mode: 'ui',
  revision: 1,
  routes: [],
  secrets: [],
  callers: [],
  admin_keys: [],
  csrf: 'usage-test-csrf',
  has_native_usage: true,
};
const empty: ProxyUsageSnapshot = { interval_minutes: 15, refreshing: false, providers: [] };
const responses: Record<string, unknown> = {
  'auth/status': { auth: 'token', csrf: 'auth-csrf' },
  timeline: { timezone: 'UTC', range: '30d', start: '', end: '', currencies: [], buckets: [] },
  summary: { timezone: 'UTC', devices: [], apps: [], providers: [] },
};
const cleanup: (() => void)[] = [];
afterEach(() => {
  for (const dispose of cleanup.splice(0)) dispose();
  vi.useRealTimers();
});

function setup(usage: () => Response | Promise<Response>, snapshot = state) {
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), window.location.origin);
    expect(url.pathname).toMatch(/^\/_proxy\/api\//);
    switch (url.pathname.slice('/_proxy/api/'.length)) {
      case 'state':
        return Response.json(snapshot);
      case 'usage':
        return usage();
      case 'usage/refresh':
        expect(init?.method).toBe('POST');
        expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe(state.csrf);
        return new Response(null, { status: 202 });
      case 'usage/settings':
        expect(init?.method).toBe('PUT');
        expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe(state.csrf);
        return new Response(null, { status: 204 });
      default:
        return Response.json(responses[url.pathname.slice('/_proxy/api/'.length)] ?? {});
    }
  });
  window.history.replaceState(null, '', '/_proxy/?tab=usage');
  const root = document.createElement('div');
  document.body.append(root);
  const unmount = mountProxy(root, new ProxyClient(fetcher));
  cleanup.push(() => {
    unmount();
    root.remove();
  });
  return { root, fetcher };
}

function usageRequests(fetcher: ReturnType<typeof setup>['fetcher']) {
  return fetcher.mock.calls.filter(([path]) => path === '/_proxy/api/usage');
}

describe('proxy native usage tab', () => {
  it('normalizes an unsupported Usage bookmark and does not fetch usage', async () => {
    const { root, fetcher } = setup(() => Response.json(empty), {
      ...state,
      has_native_usage: undefined,
    });
    await waitFor(() =>
      expect(getByRole(root, 'button', { name: 'Home' }).getAttribute('aria-current')).toBe('page'),
    );
    expect(queryByRole(root, 'button', { name: /^Usage$/ })).toBeNull();
    expect(new URL(window.location.href).searchParams.has('tab')).toBe(false);
    expect(usageRequests(fetcher)).toHaveLength(0);
  });

  it.each([
    ['claude-bin', 'Claude Code', false],
    ['claude-bin', 'Work Claude plan', true],
  ] as const)('names the Usage row %s and describes it as %s', async (id, label, renamed) => {
    const { root } = setup(
      () =>
        Response.json({
          ...empty,
          providers: [{ id, report: { groups: [], detail: 'Quota details' } }],
        } satisfies ProxyUsageSnapshot),
      {
        ...state,
        providers: renamed
          ? [{ id, label, routes: [id], family: 'inference', status: 'green', reason: '' }]
          : [],
      },
    );
    const table = await waitFor(() => getByRole(root, 'table', { name: 'Native usage limits' }));
    const heading = within(table).getByRole('rowheader');
    expect(heading.firstChild?.textContent).toBe(id);
    expect(heading).toHaveTextContent(label);
    expect(getByText(root, `${id}:`, { exact: true })).toBeVisible();
  });

  it('keeps every native window and description visible, including non-percent and disabled limits', async () => {
    const note = 'Generic plan description. '.repeat(10).trim();
    const now = Date.now();
    const { root } = setup(() =>
      Response.json({
        ...empty,
        providers: [
          {
            id: 'agy-bin',
            updated_at: new Date(now).toISOString(),
            report: {
              plan: 'Pro',
              detail: note,
              groups: [
                {
                  name: 'Gemini models',
                  detail: 'Neon · Hydrogen',
                  windows: [
                    {
                      label: '6 hour window',
                      duration_minutes: 360,
                      used_percent: 15,
                      resets_at: new Date(now + 180 * 60_000).toISOString(),
                    },
                    {
                      label: 'Credits',
                      duration_minutes: 0,
                      used_percent: 0,
                      percent_unknown: true,
                      detail: '78 remaining',
                    },
                    {
                      label: 'Bonus',
                      duration_minutes: 0,
                      used_percent: 0,
                      disabled: true,
                      detail: 'Unavailable on this plan',
                    },
                  ],
                },
              ],
            },
          },
        ],
      } satisfies ProxyUsageSnapshot),
    );
    const table = await waitFor(() => getByRole(root, 'table', { name: 'Native usage limits' }));
    expect(table.querySelectorAll('tbody tr')).toHaveLength(3);
    expect(within(table).getByText('Neon · Hydrogen')).toBeTruthy();
    expect(within(table).getByText('6 hours')).toBeTruthy();
    expect(within(table).getByText('15%')).toBeTruthy();
    expect(within(table).getByRole('cell', { name: /^15%\s*used$/ })).toBeVisible();
    expect(table.querySelectorAll('.usage-bar-pace')).toHaveLength(1);
    expect(within(table).getByText('78 remaining')).toBeTruthy();
    expect(within(table).getByText('Disabled')).toBeTruthy();
    expect(within(table).getByText('Unavailable on this plan')).toBeTruthy();
    expect(within(table).queryByText('0%')).toBeNull();
    expect(table.textContent).not.toContain(note);
    expect(getByText(root, note).closest('.usage-report-notes')).toBeTruthy();
  });

  it('replaces an exhausted reading with Previous window when its reset passes', async () => {
    vi.useFakeTimers();
    const now = Date.now();
    const { root } = setup(() =>
      Response.json({
        ...empty,
        providers: [
          {
            id: 'agy-bin',
            updated_at: new Date(now).toISOString(),
            report: {
              groups: [
                {
                  name: 'Neon',
                  windows: [
                    {
                      label: 'Daily',
                      duration_minutes: 0,
                      used_percent: 100,
                      detail: '0 remaining',
                      resets_at: new Date(now + 30_000).toISOString(),
                    },
                  ],
                },
              ],
            },
          },
        ],
      } satisfies ProxyUsageSnapshot),
    );
    await vi.advanceTimersByTimeAsync(0);
    expect(getByText(root, 'Limit reached')).toBeTruthy();
    expect(getByText(root, '100%')).toBeTruthy();
    await vi.advanceTimersByTimeAsync(30_000);
    const row = within(getByRole(root, 'row', { name: /Previous window/ }));
    expect(row.getByText('Reset passed')).toBeTruthy();
    for (const text of ['100%', '0 remaining', 'Limit reached'])
      expect(row.queryByText(text)).toBeNull();
  });

  it('uses the stored setting, validates its bounds, and requests refresh separately', async () => {
    const { root, fetcher } = setup(() => Response.json(empty));
    await waitFor(() => getByText(root, 'No native usage reported.'));
    const interval = getByRole<HTMLInputElement>(root, 'spinbutton', {
      name: 'Interval (min)',
    });
    expect(interval.value).toBe('15');
    const save = getByRole(root, 'button', { name: 'Save interval' });
    for (const invalid of ['9', '1441', '10.5']) {
      fireEvent.input(interval, { target: { value: invalid } });
      fireEvent.click(save);
    }
    expect(fetcher.mock.calls.some(([url]) => url === '/_proxy/api/usage/settings')).toBe(false);
    fireEvent.input(interval, { target: { value: '1440' } });
    fireEvent.click(save);
    await waitFor(() => expect(usageRequests(fetcher)).toHaveLength(2));
    expect(
      fetcher.mock.calls.find(([url]) => url === '/_proxy/api/usage/settings')?.[1]?.body,
    ).toBe('{"interval_minutes":1440}');
    const refresh = getByRole<HTMLButtonElement>(root, 'button', { name: 'Refresh usage' });
    await waitFor(() => expect(refresh.disabled).toBe(false));
    fireEvent.click(refresh);
    await waitFor(() => expect(usageRequests(fetcher)).toHaveLength(3));
    expect(fetcher.mock.calls.some(([url]) => url === '/_proxy/api/usage/refresh')).toBe(true);
  });

  it.each(['settings', 'refresh'])(
    'honors %s during a GET and ignores its late result',
    async (action) => {
      let finishRead!: (response: Response) => void;
      const pending = new Promise<Response>((resolve) => {
        finishRead = resolve;
      });
      const { root, fetcher } = setup(
        vi
          .fn<() => Response | Promise<Response>>()
          .mockReturnValueOnce(pending)
          .mockImplementation(() => Response.json({ ...empty, interval_minutes: 30 })),
      );
      await waitFor(() => expect(usageRequests(fetcher)).toHaveLength(1));
      const interval = getByRole<HTMLInputElement>(root, 'spinbutton');
      if (action === 'settings') fireEvent.input(interval, { target: { value: '30' } });
      fireEvent.click(
        getByRole(root, 'button', {
          name: action === 'settings' ? 'Save interval' : 'Refresh usage',
        }),
      );
      await waitFor(() => getByText(root, 'No native usage reported.'));
      expect(
        fetcher.mock.calls.filter(([path]) => path === `/_proxy/api/usage/${action}`),
      ).toHaveLength(1);
      expect(usageRequests(fetcher)[0][1]?.signal?.aborted).toBe(true);
      finishRead(Response.json({ ...empty, interval_minutes: 90, refreshing: true }));
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(interval.value).toBe('30');
      expect(getByRole<HTMLButtonElement>(root, 'button', { name: 'Refresh usage' }).disabled).toBe(
        false,
      );
    },
  );

  it('stops quick checks on a failed read and clears errors after recovery', async () => {
    vi.useFakeTimers();
    const { root, fetcher } = setup(
      vi
        .fn<() => Response>()
        .mockReturnValueOnce(Response.json({ ...empty, refreshing: true }))
        .mockReturnValueOnce(new Response(null, { status: 503 }))
        .mockImplementation(() => Response.json(empty)),
    );
    await vi.advanceTimersByTimeAsync(1_000);
    const refresh = getByRole<HTMLButtonElement>(root, 'button', { name: 'Refresh usage' });
    expect(refresh.disabled).toBe(false);
    expect(getByText(root, 'Could not load usage. Showing last loaded data.')).toBeTruthy();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(usageRequests(fetcher)).toHaveLength(2);
    fetcher.mockResolvedValueOnce(
      new Response(null, { status: 429, headers: { 'Retry-After': '42' } }),
    );
    fireEvent.click(refresh);
    await vi.advanceTimersByTimeAsync(0);
    expect(getByText(root, /refresh is on cooldown/)).toBeTruthy();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(getByText(root, 'No native usage reported.')).toBeTruthy();
    expect(usageRequests(fetcher)).toHaveLength(3);
  });

  it('retains last snapshots after errors without showing stale forecasts', async () => {
    const now = Date.now();
    const { root } = setup(
      vi
        .fn<() => Response>()
        .mockReturnValueOnce(
          Response.json({
            ...empty,
            providers: [
              {
                id: 'chatgpt',
                updated_at: new Date(now).toISOString(),
                report: {
                  groups: [
                    {
                      name: 'Codex',
                      windows: [
                        {
                          label: 'Weekly',
                          duration_minutes: 10080,
                          used_percent: 71,
                          resets_at: new Date(now + 28 * 60 * 60_000).toISOString(),
                        },
                      ],
                    },
                  ],
                },
              },
            ],
          } satisfies ProxyUsageSnapshot),
        )
        .mockReturnValue(new Response(null, { status: 503 })),
    );
    await waitFor(() => getByText(root, '15% left'));
    fireEvent.click(getByRole(root, 'button', { name: 'Refresh usage' }));
    await waitFor(() => getByText(root, 'Could not load usage. Showing last loaded data.'));
    expect(getByText(root, 'Stale snapshot').hidden).toBe(false);
    expect(getByText(root, '71%')).toBeTruthy();
    expect(within(root).queryByText('15% left')).toBeNull();
  });

  it('bounds rapid status checks and aborts a pending read on tab exit', async () => {
    vi.useFakeTimers();
    let pending = false;
    const { root, fetcher } = setup(() =>
      pending ? new Promise<Response>(() => {}) : Response.json({ ...empty, refreshing: true }),
    );
    await vi.advanceTimersByTimeAsync(0);
    expect(usageRequests(fetcher)).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(21_000);
    expect(usageRequests(fetcher)).toHaveLength(21);
    expect(getByText(root, 'Refresh still running.')).toBeTruthy();
    pending = true;
    await vi.advanceTimersByTimeAsync(60_000);
    const signal = usageRequests(fetcher).at(-1)?.[1]?.signal;
    expect(signal?.aborted).toBe(false);
    fireEvent.click(getByRole(root, 'button', { name: 'Providers' }));
    expect(signal?.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(usageRequests(fetcher)).toHaveLength(22);
  });
});
