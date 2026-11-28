import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, getByLabelText, getByRole, waitFor } from '@testing-library/preact';
import { ProxyClient, type ProxyRequest, type ProxyState } from '../src/api/proxy-client';
import { mountProxy } from './app';
import { requestDetail as renderRequestDetail, requestPageNumbers } from './requests';

const snapshot: ProxyState = {
  defaults: null,
  providers: [],
  auth: 'token',
  mode: 'ui',
  revision: 1,
  routes: [],
  callers: [],
  admin_keys: [],
  secrets: [],
  csrf: 'test',
};
const requestDetail = (request: ProxyRequest) => renderRequestDetail(request, { toast: vi.fn() });

function record(seq: number): ProxyRequest {
  return {
    id: `request-${seq}`,
    seq,
    started: 1700000000000,
    ended: 1700000000010,
    caller_id: 'caller',
    caller_name: 'Client',
    route: 'route',
    revision: 1,
    method: 'POST',
    path: `/request-${seq}`,
    peer: '127.0.0.1',
    status: 200,
    outcome: 'complete',
    sent: 3,
    received: 4,
    header_ms: 1,
    first_byte_ms: 2,
    total_ms: 10,
    upstream_id: '',
  };
}
let dispose: (() => void) | undefined;
function setup(total = 2225) {
  window.history.replaceState(null, '', '/_proxy/?tab=requests');
  const api = new ProxyClient(vi.fn());
  vi.spyOn(api, 'authStatus').mockResolvedValue({
    auth: 'token',
    setup_required: false,
    bootstrap_available: false,
    recovery_available: false,
    bootstrap_verified: false,
    recovery_verified: false,
    csrf: 'test',
    origin: 'http://localhost',
  });
  vi.spyOn(api, 'state').mockResolvedValue(snapshot);
  const requests = vi.spyOn(api, 'requests').mockImplementation(async (filters) => {
    const page = Number(filters.page);
    const offset = (page - 1) * 25;
    return {
      requests: Array.from({ length: Math.max(0, Math.min(25, total - offset)) }, (_, i) =>
        record(total - offset - i),
      ),
      next: total - offset > 25 ? total - offset - 24 : 0,
      total,
      page,
      page_size: 25,
    };
  });
  const root = document.createElement('div');
  document.body.append(root);
  dispose = mountProxy(root, api);
  return { root, api, requests };
}
function pager(root: HTMLElement, bottom = false) {
  return getByRole(root, 'navigation', {
    name: bottom ? 'Request pages (bottom)' : 'Request pages',
  });
}
async function ready(root: HTMLElement, page = 1) {
  await waitFor(() =>
    expect(getByRole(pager(root), 'button', { name: `Page ${page}` })).toHaveAttribute(
      'aria-current',
      'page',
    ),
  );
  await waitFor(() =>
    expect(root.querySelector('.request-list')).toHaveAttribute('aria-busy', 'false'),
  );
}
afterEach(() => {
  dispose?.();
  document.body.replaceChildren();
  window.history.replaceState(null, '', '/');
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it('renders the saved client name and tag even after the client is deleted', async () => {
  const { root, requests } = setup(1);
  const request = {
    ...record(1),
    caller_id: 'c_deleted',
    caller_name: 'Deleted client',
    tag: 'lab',
  };
  requests.mockImplementation(async () => ({
    requests: [request],
    next: 0,
    total: 1,
    page: 1,
    page_size: 25,
  }));
  // The state fixture has no callers. The ledger must use the request snapshot.
  await waitFor(() => {
    const cell = root.querySelector('.request-client');
    expect(cell).toHaveTextContent('Deleted client · lab');
    expect(cell).toHaveAttribute('title', 'Client: Deleted client · lab · Route: route');
    expect(cell).toHaveAttribute('aria-label', 'Client: Deleted client · lab · Route: route');
  });
  expect(requestDetail(request)).toHaveTextContent('Deleted client · lab');
});

describe('first-page polling', () => {
  async function mount() {
    vi.useFakeTimers();
    const fixture = setup(26);
    await vi.advanceTimersByTimeAsync(0);
    expect(fixture.root.querySelectorAll('.request-row')).toHaveLength(25);
    return fixture;
  }

  it('polls once a second, preserves unchanged rows and controls, and shows changed rows', async () => {
    const { root, requests } = await mount();
    const row = root.querySelector('.request-row');
    const next = getByRole(pager(root), 'button', { name: 'Next' });
    await vi.advanceTimersByTimeAsync(999);
    expect(requests).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(requests).toHaveBeenCalledTimes(2);
    expect(root.querySelector('.request-row')).toBe(row);
    expect(getByRole(pager(root), 'button', { name: 'Next' })).toBe(next);

    const active = { ...record(26), status: 0, outcome: 'active' };
    requests.mockResolvedValueOnce({ requests: [active], next: 0, total: 1 });
    await vi.advanceTimersByTimeAsync(1000);
    expect(root.querySelector('.request-row')).not.toBe(row);
    expect(root.querySelector('.request-status')).toHaveTextContent('Active');

    requests.mockResolvedValueOnce({ requests: [record(26)], next: 0, total: 1 });
    await vi.advanceTimersByTimeAsync(1000);
    expect(root.querySelector('.request-status')).toHaveTextContent('200');
  });

  it('uses applied filters and does not poll on older pages, when hidden, or after navigation', async () => {
    const { root, requests } = await mount();
    const filters = root.querySelector<HTMLDetailsElement>('.request-filters')!;
    filters.open = true;
    fireEvent.input(getByLabelText(filters, 'Path'), { target: { value: '/applied' } });
    fireEvent.click(getByRole(filters, 'button', { name: 'Apply filters' }));
    await vi.advanceTimersByTimeAsync(0);
    fireEvent.input(getByLabelText(filters, 'Path'), { target: { value: '/draft' } });
    await vi.advanceTimersByTimeAsync(1000);
    expect(requests).toHaveBeenLastCalledWith(
      { path: '/applied', page: '1', page_size: '25' },
      expect.any(AbortSignal),
    );
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(requests).toHaveBeenCalledTimes(4);
    expect(requests).toHaveBeenLastCalledWith({ path: '/applied', page: '2', page_size: '25' });

    fireEvent.click(getByRole(pager(root), 'button', { name: 'Previous' }));
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    await vi.advanceTimersByTimeAsync(2000);
    expect(requests).toHaveBeenCalledTimes(5);
    hidden.mockReturnValue(false);
    await vi.advanceTimersByTimeAsync(1000);
    expect(requests).toHaveBeenCalledTimes(6);
    fireEvent.click(getByRole(root, 'button', { name: 'Providers' }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(requests).toHaveBeenCalledTimes(6);
  });

  it('keeps detail open when a poll changes and discards an in-flight poll on pagination', async () => {
    const { root, requests } = await mount();
    const open = getByRole(root, 'button', { name: 'POST /request-26' });
    fireEvent.click(open);
    const detail = root.querySelector('.request-detail');
    requests.mockResolvedValueOnce({ requests: [record(99)], next: 0, total: 1 });
    await vi.advanceTimersByTimeAsync(1000);
    expect(root.querySelector('.request-detail')).toBe(detail);

    let resolvePoll!: (page: { requests: ProxyRequest[]; next: number }) => void;
    requests.mockImplementationOnce(() => new Promise((resolve) => (resolvePoll = resolve)));
    await vi.advanceTimersByTimeAsync(2000);
    expect(requests).toHaveBeenCalledTimes(3);
    const signal = requests.mock.lastCall?.[1];
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await vi.advanceTimersByTimeAsync(0);
    expect(signal?.aborted).toBe(true);
    resolvePoll({ requests: [record(99)], next: 0 });
    await vi.advanceTimersByTimeAsync(1000);
    expect(pager(root)).toHaveTextContent('Page 2');
    expect(root.querySelector('.request-open')).toHaveAccessibleName('POST /request-1');
  });
});

describe('request page windows', () => {
  it.each([
    [1, 1, [1]],
    [1, 5, [1, 2, 3, 4, 5]],
    [1, 89, [1, 2, 3, 4, 5, '…', 88, 89]],
    [45, 89, [1, 2, '…', 44, 45, 46, '…', 88, 89]],
    [89, 89, [1, 2, '…', 85, 86, 87, 88, 89]],
    [4, 8, [1, 2, 3, 4, 5, 6, 7, 8]],
  ])('shows a bounded traditional page window at page %i of %i', (current, total, expected) => {
    expect(requestPageNumbers(current as number, total as number)).toEqual(expected);
  });
});

describe('numbered request pagination', () => {
  it('shows compact, collapsed filters, 25 rows, and numbered controls above and below', async () => {
    const { root } = setup();
    await ready(root);
    expect(root.querySelector('.request-filters')).not.toHaveAttribute('open');
    expect(root.querySelector('.requests-header > small')?.textContent).toBe('30-day history');
    expect(root.querySelectorAll('.request-row')).toHaveLength(25);
    expect(pager(root).textContent).toContain('1–25 of 2,225 requests · Page 1 of 89');
    expect(pager(root, true).textContent).toContain('Page 1 of 89');
    expect(getByRole(pager(root), 'button', { name: 'Previous' })).toBeDisabled();
    expect(getByRole(pager(root), 'button', { name: 'Page 89' })).toBeEnabled();
    expect(root.querySelector('header')?.textContent).not.toMatch(/managed|Revision/);
  });
  it('uses the numbered footer to jump to the last page and replaces rather than appending', async () => {
    const { root, requests } = setup();
    await ready(root);
    fireEvent.click(getByRole(root, 'button', { name: 'POST /request-2225' }));
    expect(root.querySelector('.request-detail')).not.toBeNull();
    fireEvent.click(getByRole(pager(root, true), 'button', { name: 'Page 89' }));
    await ready(root, 89);
    expect(requests).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: '89', page_size: '25' }),
    );
    expect(root.querySelectorAll('.request-row')).toHaveLength(25);
    expect(root.querySelector('.request-detail')).toBeNull();
    expect(pager(root).textContent).toContain('2,201–2,225 of 2,225 requests');
    expect(getByRole(pager(root), 'button', { name: 'Next' })).toBeDisabled();
    fireEvent.click(getByRole(pager(root, true), 'button', { name: 'Previous' }));
    await ready(root, 88);
    expect(requests).toHaveBeenLastCalledWith(expect.objectContaining({ page: '88' }));
    expect(document.activeElement).toBe(pager(root).querySelector('[role=status]'));
  });
  it('uses applied filters for the badge, export, Refresh and paging until the draft is applied', async () => {
    const { root, api, requests } = setup();
    const requestExport = vi.spyOn(api, 'requestExport').mockResolvedValue('{"requests":[]}');
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:requests');
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await ready(root);
    const filters = root.querySelector('details.request-filters') as HTMLDetailsElement;
    const summary = filters.querySelector('summary');
    const refresh = getByRole(root, 'button', { name: 'Refresh' });
    expect(refresh).toHaveClass('request-refresh');
    expect(refresh.closest('details')).toBeNull();
    filters.open = true;
    const method = getByLabelText(root, 'Method');
    const path = getByLabelText(root, 'Path');
    const id = getByLabelText(root, 'Request ID');
    const apply = getByRole(root, 'button', { name: 'Apply filters' });
    fireEvent.input(method, { target: { value: 'get' } });
    expect(summary).toHaveTextContent(/^Filters$/);
    fireEvent.click(apply);
    await ready(root);
    expect(requests).toHaveBeenLastCalledWith({ method: 'GET', page: '1', page_size: '25' });
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await ready(root, 2);

    const visibleRow = root.querySelector('.request-row');
    fireEvent.input(method, { target: { value: ' post ' } });
    fireEvent.input(path, { target: { value: '/draft' } });
    fireEvent.input(id, { target: { value: 'draft-id' } });
    expect(summary).toHaveTextContent('Filters (1)');
    expect(root.querySelector('.request-row')).toBe(visibleRow);
    expect(requests).toHaveBeenCalledTimes(3);
    fireEvent.click(getByRole(root, 'button', { name: /^Export JSON/ }));
    await waitFor(() =>
      expect(root.querySelector('.toast-region')).toHaveTextContent(
        'Matching request metadata exported',
      ),
    );
    expect(requestExport).toHaveBeenCalledExactlyOnceWith({ method: 'GET' });

    filters.open = false;
    fireEvent.click(refresh);
    await ready(root, 2);
    expect(requests).toHaveBeenLastCalledWith({ method: 'GET', page: '2', page_size: '25' });
    expect(filters.open).toBe(false);
    fireEvent.click(getByRole(pager(root, true), 'button', { name: 'Next' }));
    await ready(root, 3);
    expect(requests).toHaveBeenLastCalledWith({ method: 'GET', page: '3', page_size: '25' });
    filters.open = true;
    expect(method).toHaveValue(' post ');
    expect(path).toHaveValue('/draft');
    expect(id).toHaveValue('draft-id');
    fireEvent.input(id, { target: { value: '' } });
    let complete!: (value: Awaited<ReturnType<ProxyClient['requests']>>) => void;
    requests.mockReturnValueOnce(
      new Promise((resolve) => {
        complete = resolve;
      }),
    );
    const previousRow = root.querySelector('.request-row');
    fireEvent.click(apply);
    expect(requests).toHaveBeenLastCalledWith({
      method: 'POST',
      path: '/draft',
      page: '1',
      page_size: '25',
    });
    expect(summary).toHaveTextContent('Filters (1)');
    expect(root.querySelector('.request-row')).toBe(previousRow);
    expect(pager(root)).toHaveTextContent('Page 3');
    complete({ requests: [record(42)], next: 0, total: 1, page: 1 });
    await ready(root);
    expect(summary).toHaveTextContent('Filters (2)');
    expect(getByRole(root, 'button', { name: 'POST /request-42' })).toBeVisible();
  });
  it('preserves the last successful query and rows on failed Apply and Clear, then clears both states on success', async () => {
    const { root, requests } = setup();
    await ready(root);
    const filters = root.querySelector('details.request-filters') as HTMLDetailsElement;
    const summary = filters.querySelector('summary');
    filters.open = true;
    const method = getByLabelText(root, 'Method');
    const outcome = getByLabelText(root, 'Outcome');
    const apply = getByRole(root, 'button', { name: 'Apply filters' });
    const clear = getByRole(root, 'button', { name: 'Clear' });
    fireEvent.input(method, { target: { value: 'get' } });
    fireEvent.click(apply);
    await ready(root);
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await ready(root, 2);
    const visibleRow = root.querySelector('.request-row');
    fireEvent.input(method, { target: { value: 'post' } });
    fireEvent.change(outcome, { target: { value: 'http_error' } });

    for (const [control, draftQuery] of [
      [apply, { method: 'POST', outcome: 'http_error' }],
      [clear, {}],
    ] as const) {
      requests.mockRejectedValueOnce(new Error(`${control.textContent} unavailable`));
      fireEvent.click(control);
      await ready(root, 2);
      expect(requests).toHaveBeenLastCalledWith({ ...draftQuery, page: '1', page_size: '25' });
      expect(root).toHaveTextContent(`${control.textContent} unavailable`);
      expect(summary).toHaveTextContent('Filters (1)');
      expect(root.querySelector('.request-row')).toBe(visibleRow);
      expect(method).toHaveValue('post');
      expect(outcome).toHaveValue('http_error');
    }
    fireEvent.click(getByRole(root, 'button', { name: 'Refresh' }));
    await ready(root, 2);
    expect(requests).toHaveBeenLastCalledWith({ method: 'GET', page: '2', page_size: '25' });

    fireEvent.click(clear);
    await ready(root);
    expect(requests).toHaveBeenLastCalledWith({ page: '1', page_size: '25' });
    expect(summary).toHaveTextContent(/^Filters$/);
    expect(method).toHaveValue('');
    expect(outcome).toHaveValue('');
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await ready(root, 2);
    expect(requests).toHaveBeenLastCalledWith({ page: '2', page_size: '25' });
  });
  it('validates the date range when applying, but treats a Request ID as the only applied field', async () => {
    const { root, api, requests } = setup();
    const requestExport = vi.spyOn(api, 'requestExport');
    await ready(root);
    const filters = root.querySelector('details.request-filters') as HTMLDetailsElement;
    const summary = filters.querySelector('summary');
    filters.open = true;
    const apply = getByRole(root, 'button', { name: 'Apply filters' });
    fireEvent.input(getByLabelText(root, 'Method'), { target: { value: 'get' } });
    fireEvent.input(getByLabelText(root, 'Since (local time)'), {
      target: { value: '2026-11-12T09:00' },
    });
    fireEvent.input(getByLabelText(root, 'Until (local time)'), {
      target: { value: '2026-11-11T09:00' },
    });
    fireEvent.click(apply);
    await ready(root);
    expect(root).toHaveTextContent('Since must be earlier than Until.');
    expect(summary).toHaveTextContent(/^Filters$/);
    expect(requests).toHaveBeenCalledTimes(1);

    const id = getByLabelText(root, 'Request ID');
    fireEvent.input(id, { target: { value: ' request-42 ' } });
    fireEvent.click(apply);
    await ready(root);
    expect(summary).toHaveTextContent('Filters (1)');
    expect(requests).toHaveBeenLastCalledWith({ id: 'request-42', page: '1', page_size: '25' });
    fireEvent.input(id, { target: { value: '' } });
    fireEvent.click(getByRole(root, 'button', { name: /^Export JSON/ }));
    await waitFor(() => expect(root).toHaveTextContent('Clear the applied Request ID'));
    expect(requestExport).not.toHaveBeenCalled();
  });
  it('keeps the current page and shows the error when a page cannot be fetched', async () => {
    const { root, requests } = setup();
    await ready(root);
    requests.mockRejectedValueOnce(new Error('Page unavailable'));
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await ready(root);
    expect(root.querySelectorAll('.request-row')).toHaveLength(25);
    expect(root.textContent).toContain('Page unavailable');
    expect(getByRole(pager(root), 'button', { name: 'Next' })).toBeEnabled();
    expect(document.activeElement).toBe(root.querySelector(':scope > [role=status]'));
  });
  it('disables both pagers during a fetch and ignores repeated clicks', async () => {
    const { root, requests } = setup();
    await ready(root);
    let complete!: (value: Awaited<ReturnType<ProxyClient['requests']>>) => void;
    requests.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve;
        }),
    );
    const next = getByRole(pager(root), 'button', { name: 'Next' });
    fireEvent.click(next);
    fireEvent.click(next);
    expect(requests).toHaveBeenCalledTimes(2);
    for (const button of root.querySelectorAll('.request-pagination button'))
      expect(button).toBeDisabled();
    expect(root.querySelector('.request-refresh')).toBeDisabled();
    complete({ requests: [record(2200)], next: 0, page: 2, page_size: 25, total: 26 });
    await ready(root, 2);
    expect(root.querySelector('.request-refresh')).toBeEnabled();
    expect(root.querySelectorAll('.request-row')).toHaveLength(1);
    expect(pager(root).textContent).toContain('26–26 of 26 requests · Page 2 of 2');
  });
  it('uses the server-clamped page when retention removes later pages', async () => {
    const { root, requests } = setup();
    await ready(root);
    requests.mockResolvedValueOnce({
      requests: [record(1)],
      next: 0,
      page: 1,
      page_size: 25,
      total: 1,
    });
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Page 89' }));
    await waitFor(() =>
      expect(pager(root).textContent).toContain('1–1 of 1 request · Page 1 of 1'),
    );
    await ready(root);
    expect(getByRole(pager(root), 'button', { name: 'Next' })).toBeDisabled();
  });
  it('shows an empty first page without inventing other pages', async () => {
    const { root } = setup(0);
    await ready(root);
    expect(pager(root).textContent).toContain('0–0 of 0 requests · Page 1 of 1');
    expect(getByRole(pager(root), 'button', { name: 'Next' })).toBeDisabled();
    expect(getByRole(pager(root), 'button', { name: 'Previous' })).toBeDisabled();
    expect(root.textContent).toContain('No matching requests');
  });
});

function detailGroup(root: HTMLElement, heading: string): HTMLElement {
  return getByRole(root, 'heading', { name: heading, hidden: true }).parentElement!;
}
function detailValues(root: HTMLElement): Record<string, string | null> {
  return Object.fromEntries(
    [...root.querySelectorAll('dt')].map((term) => [
      term.textContent,
      term.nextElementSibling?.textContent ?? null,
    ]),
  );
}
describe('provider-reported request usage', () => {
  it('shows the endpoint and compact transfer sizes when model and tokens are unavailable', async () => {
    const { root, requests } = setup(1);
    const request: ProxyRequest = {
      ...record(1),
      route: 'chatgpt',
      method: 'GET',
      local_prefix: '/chatgpt/',
      path: '/models',
      sent: 0,
      received: 12_230,
      usage: { usage: { input: -1, source: 'not_applicable' } },
      money: { billing: 'plan', kind: 'unmeasured' },
    };
    requests.mockResolvedValueOnce({ requests: [request], next: 0, total: 1, page: 1 });
    await ready(root);
    const open = getByRole(root, 'button', { name: 'GET /chatgpt/models' });
    expect(open).toHaveTextContent('GET /chatgpt/models');
    expect(root.querySelector('.request-input')).toHaveTextContent(/^0 B$/);
    expect(root.querySelector('.request-output')).toHaveTextContent(/^~12.23K$/);
    expect(root.querySelector('.request-output')).toHaveAttribute(
      'title',
      `Bytes out (received from upstream): ${(12_230).toLocaleString()} bytes`,
    );
    expect(root.querySelector('.request-mobile-usage')).toHaveTextContent(/^0 B→~12.23K$/);
    fireEvent.click(open);
    const detail = root.querySelector<HTMLElement>('.request-detail')!;
    expect(detailValues(detail).Endpoint).toBe('GET /chatgpt/models');
    expect(detailValues(detail)['Sent upstream']).toBe('0 B');
    expect(detailValues(detail)['Received upstream']).toBe('~12.23K');
    expect(detail.querySelector('.request-tokens')).toBeNull();
    expect(detail).not.toHaveTextContent(
      /Model not reported|API-equivalent: Not available|Cache overlap/,
    );
  });
  it('does not invent transfer sizes for active requests or unknown byte counters', () => {
    expect(
      requestDetail({ ...record(1), outcome: 'active', sent: 0, received: 0 }),
    ).not.toHaveTextContent('0 B');
    const detail = requestDetail({ ...record(1), sent: -1, received: 18_200_000 });
    expect(detailValues(detail)['Received upstream']).toBe('~18.2M');
    expect(detailValues(detail)['Sent upstream']).toBe('—');
  });
  it('preserves partial reported zeros, unknown sentinels, missing fields and exact model labels', () => {
    const request = record(1);
    request.usage = {
      protocol: 'responses',
      usage: {
        model: 'gpt-5.2-codex-2026-11-01',
        input: 0,
        output: -1,
        cached_input: 0,
        source: 'partial',
      },
    };
    const detail = requestDetail(request);
    const values = detailValues(detail);
    expect(detailValues(detailGroup(detail, 'Response')).Model).toBe('gpt-5.2-codex-2026-11-01');
    expect(values['Input']).toBe('≥0 · partial');
    expect(values['Output']).toBe('Not reported');
    expect(values['Cache read']).toBe('≥0 · partial');
    expect(values['Cache write']).toBe('Not reported');
    expect(values.Evidence).toBe('partial');
  });
  it('retains known counters even when overall evidence and identity are uncertain', () => {
    const request = record(1);
    request.usage = { usage: { input: 1234, output: 0, source: 'unknown', ambiguous: true } };
    const detail = requestDetail(request);
    const values = detailValues(detail);
    expect(values['Input']).toBe((1234).toLocaleString());
    expect(values['Output']).toBe('0');
    expect(detailValues(detail).Endpoint).toBe('POST /request-1');
    expect(values.Evidence).toBe('unknown · ambiguous model/tier');
    expect(detailValues(detailGroup(detail, 'Response')).Model).toBe('Ambiguous');
  });
  it.each(['responses', 'chat_completions', 'gemini', 'messages', 'bedrock'] as const)(
    'explains %s cache conventions without synthesizing totals',
    (protocol) => {
      const request = record(1);
      request.usage = {
        protocol,
        usage: {
          input: 100,
          output: 20,
          cached_input: 40,
          cache_write: 10,
          reasoning: 3,
          source: 'reported',
        },
      };
      const detail = requestDetail(request);
      const group = detailGroup(detail, 'Tokens');
      const values = detailValues(group);
      expect(values['Input']).toBe('100');
      expect(values['Cache read']).toBe('40');
      expect(detailValues(detail)['Cache write']).toBe('10');
      expect(values['Reported total tokens']).toBeUndefined();
      const title = detail.querySelector('.request-tokens dl > div')!.getAttribute('title');
      expect(title).toContain(
        protocol === 'messages' || protocol === 'bedrock'
          ? 'Input excludes cache reads and writes'
          : 'Cache reads included in input',
      );
    },
  );
  it('preserves every reported zero token counter', () => {
    const request = record(1);
    request.usage = {
      protocol: 'messages',
      usage: { input: 0, output: 0, cached_input: 0, cache_write: 0, source: 'reported' },
    };
    const detail = requestDetail(request);
    const values = detailValues(detail);
    for (const label of ['Input', 'Output', 'Cache read', 'Cache write'])
      expect(values[label]).toBe('0');
  });
  it('shows the exact reported model when opening a request from the compact list', async () => {
    const { root, requests } = setup(1);
    requests.mockResolvedValueOnce({
      requests: [{ ...record(1), usage: { usage: { model: 'exact-model-20261117' } } }],
      next: 0,
      total: 1,
      page: 1,
    });
    await ready(root);
    const client = root.querySelector('.request-client')!;
    expect([...client.children].map((cell) => cell.textContent)).toEqual(['Client', 'route']);
    expect(client).toHaveAttribute('aria-label', 'Client: Client · Route: route');
    expect(root.querySelector('.request-model')).toHaveTextContent('exact-model-20261117');
    expect(root.querySelector('.request-detail')).toBeNull();
    fireEvent.click(getByRole(root, 'button', { name: 'POST /request-1' }));
    expect(detailValues(detailGroup(root, 'Response')).Model).toBe('exact-model-20261117');
  });
  it('renders model text safely without deriving or displaying extra token totals', () => {
    const request = record(1);
    request.usage = { protocol: 'ollama', usage: { model: '<img src=x>', input: 1, total: 1 } };
    const detail = requestDetail(request);
    const group = detailGroup(detail, 'Tokens');
    expect(detailValues(detailGroup(detail, 'Response')).Model).toBe('<img src=x>');
    expect(detailValues(group)['Reported total tokens']).toBeUndefined();
    expect(detail.querySelector('img')).toBeNull();
  });
});

describe('concise request insights', () => {
  it.each([false, true])(
    'identifies requested-model fallbacks when ambiguous=%s',
    async (ambiguous) => {
      const { root, requests } = setup(1);
      requests.mockResolvedValueOnce({
        requests: [
          {
            ...record(1),
            request_meta: { state: 'observed', model: 'requested-only' },
            ...(ambiguous
              ? {
                  usage: {
                    protocol: 'responses',
                    usage: { ambiguous: true, source: 'reported', model: 'stale-served' },
                  },
                }
              : {}),
          },
        ],
        next: 0,
        total: 1,
        page: 1,
      });
      await ready(root);
      const open = root.querySelector('.request-open')!;
      expect(open).toHaveTextContent('requested-only');
      expect(open).not.toHaveTextContent('stale-served');
      expect(open.getAttribute('title')).toContain('Requested model: requested-only');
      expect(open.getAttribute('aria-label')).toContain('Requested model: requested-only');
    },
  );

  it('reveals aligned Requested/Response and all metadata without nested disclosures', () => {
    const request = record(1);
    request.request_meta = {
      state: 'observed',
      model: 'requested-model',
      reasoning_effort: 'high',
      thinking_budget: 0,
      output_limit: 0,
      temperature: 0,
      top_p: 0,
      stream: false,
      parallel_tools: false,
      declared_tools: 0,
      reasoning_mode: 'pro',
      reasoning_context: 'current_turn',
      reasoning_summary: 'auto',
      multi_agent: false,
      agent_limit: 3,
      cache_mode: 'explicit',
      cache_ttl: '30m',
      verbosity: 'low',
      tool_choice: 'auto',
    };
    request.usage = {
      protocol: 'responses',
      usage: {
        model: 'served-model',
        tier: 'flex',
        input: 100,
        output: 20,
        reasoning: 0,
        source: 'reported',
      },
    };
    request.response_meta = {
      state: 'observed',
      status: 'incomplete',
      incomplete_reason: 'max_output_tokens',
      cache_write_5m: 0,
      web_search_requests: 0,
    };
    const detail = requestDetail(request);
    expect(detail.querySelectorAll('details')).toHaveLength(0);
    expect(detailValues(detailGroup(detail, 'Requested'))).toMatchObject({
      Model: 'requested-model',
      Effort: 'high',
      'Output limit': '0',
      'Thinking budget': '0',
      Temperature: '0',
      'Top-p': '0',
      Stream: 'Off',
      'Parallel tools': 'Off',
      'Declared tools': '0',
      'Multi-agent': 'Off',
      'Agent limit': '3',
      'Cache policy': 'explicit',
      'Cache TTL': '30m',
      'Reasoning scope': 'current_turn',
      Summary: 'auto',
      Verbosity: 'low',
      'Tool choice': 'auto',
    });
    expect(detailValues(detailGroup(detail, 'Response'))).toMatchObject({
      Model: 'served-model',
      Tier: 'flex',
      'Cache write · 5m': '0',
      'Web search': '0',
    });
    expect(detail.querySelector('.request-meta')).toHaveTextContent('Request ID');
    expect(detail).not.toHaveTextContent(/TTFT|Technical|More settings|NEW/);
    expect(detail.querySelectorAll('.request-tokens dl > div')).toHaveLength(4);
  });
  it.each(['not_observed', 'unsupported', 'truncated', 'invalid'] as const)(
    'never interprets %s as provider defaults',
    (state) => {
      const detail = requestDetail({ ...record(1), request_meta: { state } });
      expect(detailValues(detailGroup(detail, 'Requested')).Effort).toBe('Not observed');
    },
  );
  it('distinguishes legacy unobserved metadata, absent settings, and sentinels', () => {
    expect(detailValues(detailGroup(requestDetail(record(1)), 'Requested')).Effort).toBe(
      'Not observed',
    );
    const request = {
      ...record(1),
      request_meta: {
        state: 'observed' as const,
        thinking_budget_mode: 'dynamic' as const,
        output_limit_mode: 'unlimited' as const,
      },
    };
    const values = detailValues(detailGroup(requestDetail(request), 'Requested'));
    expect(values.Effort).toBe('Not sent');
    expect(values['Thinking budget']).toBe('dynamic');
    expect(values['Output limit']).toBe('unlimited');
  });
  it('keeps copy ID immediately usable', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    try {
      const detail = requestDetail(record(1));
      document.body.append(detail);
      fireEvent.click(getByRole(detail, 'button', { name: 'Copy request ID' }));
      await waitFor(() => expect(writeText).toHaveBeenCalledWith('request-1'));
    } finally {
      vi.unstubAllGlobals();
    }
  });
  it('qualifies incomplete diagnostics without changing valid response usage', () => {
    const detail = requestDetail({
      ...record(1),
      response_meta: { state: 'partial', finish: 'length' },
      usage: { protocol: 'responses', usage: { input: 10, output: 20, source: 'reported' } },
    });
    expect(detailValues(detailGroup(detail, 'Response')).Finish).toBe('length · partial');
    expect(detailValues(detail).Output).toBe('20');
  });
});

describe('plan API-equivalent request estimate', () => {
  it.each(['estimated', 'estimated_floor'] as const)(
    'separates %s API rates from actual plan cost',
    (kind) => {
      const request = {
        ...record(1),
        money: {
          billing: 'plan' as const,
          kind: 'included' as const,
          api_equivalent: {
            kind,
            amount_micros: 1200000,
            currency: 'USD',
            source: 'Published API card',
            asof: '2026-11-17',
          },
        },
      };
      const detail = requestDetail(request);
      const response = detailValues(detailGroup(detail, 'Response'));
      expect(response['API equivalent']).toContain(
        kind === 'estimated' ? '≈$1.20 API' : '≥$1.20 API',
      );
      expect(response.Billing).toContain('extra charges not reported');
      expect(detailValues(detail)['Price source']).toBe('Published API card');
      expect(detailValues(detail)['Price date']).toBe('2026-11-17');
      expect(detail.querySelector('details')).toBeNull();
    },
  );
  it.each([undefined, { kind: 'unpriced' as const }, { kind: 'unmeasured' as const }])(
    'keeps unknown equivalence distinct from zero',
    (api_equivalent) => {
      const detail = requestDetail({
        ...record(1),
        money: { billing: 'plan', kind: 'unmeasured', api_equivalent },
      });
      expect(detailValues(detailGroup(detail, 'Response'))['API equivalent']).toBe('—');
      expect(detail).not.toHaveTextContent('$0');
    },
  );
  it('retains true zero estimates and never shows counterfactual amounts for non-plan billing', () => {
    const request: ProxyRequest = {
      ...record(1),
      money: {
        billing: 'plan',
        kind: 'included',
        api_equivalent: { kind: 'estimated', amount_micros: 0, currency: 'USD' },
      },
    };
    expect(detailValues(detailGroup(requestDetail(request), 'Response'))['API equivalent']).toBe(
      '≈$0.00 API',
    );
    request.money = {
      ...request.money!,
      billing: 'pay_as_you_go',
      kind: 'estimated',
      amount_micros: 3000000,
      currency: 'USD',
    };
    const detail = requestDetail(request);
    expect(detailValues(detailGroup(detail, 'Response')).Price).toBe('≈$3.00');
    expect(detail).not.toHaveTextContent('API equivalent');
  });
});

describe('WebSocket request metadata', () => {
  it.each([
    ['reported', 20, 2, '100→20', '20', '2'],
    ['partial', 20, 1, '≥100→≥20', '≥20 · partial', '1'],
    ['unknown', -1, undefined, '3 B→4 B', 'Not reported', undefined],
  ] as const)(
    'keeps %s response usage separate from a successful upgrade and client closure',
    async (source, output, turns, ledgerUsage, detailOutput, observedTurns) => {
      const { root, requests } = setup(1);
      requests.mockResolvedValueOnce({
        requests: [
          {
            ...record(1),
            method: 'GET',
            status: 101,
            outcome: 'closed_by_client',
            usage: { protocol: 'responses', usage: { input: 100, output, source, turns } },
          },
        ],
        next: 0,
        total: 1,
        page: 1,
      });
      await ready(root);
      expect(root.querySelector('.request-status')).toHaveTextContent(/^101 Closed by client$/);
      expect(root.querySelector('.request-status')).toHaveAttribute(
        'aria-label',
        'HTTP 101 · WebSocket upgrade succeeded · Closed by client',
      );
      expect(root.querySelector('.request-mobile-usage')).toHaveTextContent(ledgerUsage);
      fireEvent.click(getByRole(root, 'button', { name: 'GET /request-1' }));
      const values = detailValues(root.querySelector('.request-detail') as HTMLElement);
      expect(values.Status).toBe('HTTP 101 · WebSocket upgrade succeeded');
      expect(values.Outcome).toContain('Closed by client');
      expect(values.Protocol).toBe('responses');
      expect(values.Evidence).toBe(source);
      expect(values['Output']).toBe(detailOutput);
      expect(values['Observed turns']).toBe(observedTurns);
    },
  );

  it('filters closures using the exact ledger outcome value', async () => {
    const { root, requests } = setup(1);
    await ready(root);
    root.querySelector<HTMLDetailsElement>('.request-filters')!.open = true;
    const outcome = getByLabelText(root, 'Outcome');
    expect(getByRole(outcome, 'option', { name: 'Access revoked' })).toHaveValue(
      'closed_by_revocation',
    );
    fireEvent.change(outcome, { target: { value: 'closed_by_revocation' } });
    fireEvent.click(getByRole(root, 'button', { name: 'Apply filters' }));
    await waitFor(() =>
      expect(requests).toHaveBeenLastCalledWith(
        expect.objectContaining({ outcome: 'closed_by_revocation' }),
      ),
    );
  });
});

describe('Responses WebSocket turns', () => {
  function turn(outcome = 'complete'): ProxyRequest {
    return {
      ...record(2),
      method: 'GET',
      id: 'px_connection:turn-1',
      websocket_connection_id: 'px_connection',
      status: 101,
      outcome,
      sent: -1,
      received: -1,
      header_ms: -1,
      first_byte_ms: -1,
      total_ms: -1,
      usage: {
        protocol: 'responses',
        usage: { input: 100, output: 20, source: 'reported', turns: 1 },
      },
    };
  }

  it.each([
    ['complete', 'Turn complete'],
    ['incomplete', 'Turn incomplete'],
    ['unknown', 'Outcome unknown'],
  ])('shows the %s turn outcome separately from the upgrade', async (outcome, label) => {
    const { root, requests } = setup(1);
    requests.mockResolvedValueOnce({ requests: [turn(outcome)], next: 0, total: 1, page: 1 });
    await ready(root);
    const status = root.querySelector('.request-status');
    expect(status?.textContent).toBe(label);
    expect(status).toHaveAttribute(
      'aria-label',
      `${label} · Transport: HTTP 101 · WebSocket upgrade succeeded`,
    );
    expect(root.querySelector('.request-duration')).toHaveTextContent(/^—$/);
    expect(root.querySelector('.request-mobile-usage')).toHaveTextContent('100→20');
    fireEvent.click(getByRole(root, 'button', { name: 'GET /request-2' }));
    const detail = root.querySelector<HTMLElement>('.request-detail')!;
    const values = detailValues(detail);
    expect(values['Turn outcome']).toBe(label);
    expect(values.Status).toBe('HTTP 101 · WebSocket upgrade succeeded');
    expect(values['Connection outcome']).toBeUndefined();
    expect(values['Observed turns']).toBeUndefined();
    expect(values['WebSocket connection ID']).toBe('px_connection');
    for (const field of ['Headers', 'First byte', 'Duration'])
      expect(values[field]).toBe('Not reported');
  });

  it('looks up the connection capture but displays and copies the turn request ID', async () => {
    const { root, api, requests } = setup(2);
    const row = turn();
    requests.mockResolvedValueOnce({ requests: [row, record(1)], next: 0, total: 2, page: 1 });
    const lookup = vi.spyOn(api, 'debugCapture').mockImplementation(async (id) => ({
      id,
      finished: true,
      files: ['request.body'],
    }));
    await ready(root);
    fireEvent.click(getByRole(root, 'button', { name: 'GET /request-2' }));
    const detail = root.querySelector<HTMLElement>('.request-detail')!;
    await waitFor(() =>
      expect(getByRole(detail, 'button', { name: 'View raw payload' })).toBeEnabled(),
    );
    expect(lookup).toHaveBeenCalledExactlyOnceWith('px_connection');
    expect(detail).toHaveTextContent('WebSocket connection ID');
    const identifiers = detail.querySelector<HTMLElement>('.request-meta')!;
    expect(detailValues(identifiers)).toMatchObject({
      'Request ID': row.id + ' Copy request ID',
      'WebSocket connection ID': 'px_connection',
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    try {
      fireEvent.click(getByRole(identifiers, 'button', { name: 'Copy request ID' }));
      await waitFor(() => expect(writeText).toHaveBeenCalledExactlyOnceWith(row.id));
    } finally {
      vi.unstubAllGlobals();
    }

    fireEvent.click(getByRole(root, 'button', { name: 'POST /request-1' }));
    await waitFor(() => expect(lookup).toHaveBeenNthCalledWith(2, 'request-1'));
  });
});

describe('compact request ledger', () => {
  it('renders the shared desktop cells and mobile usage without raw technical noise', async () => {
    const { root, requests } = setup(1);
    requests.mockResolvedValueOnce({
      requests: [
        {
          ...record(1),
          usage: {
            protocol: 'messages',
            usage: {
              model: '<img src=x>',
              input: 2,
              cached_input: 10,
              cache_write: 1000,
              output: 19,
            },
          },
        },
      ],
      next: 0,
      total: 1,
      page: 1,
    });
    await ready(root);
    expect(
      [...root.querySelector('.request-ledger-head')!.children].map((c) => c.textContent),
    ).toEqual([
      'Time',
      'Status',
      'Model / endpoint',
      'Client / route',
      'Total in',
      'Out',
      'Cache',
      'Duration',
      'Price',
    ]);
    const headings = root.querySelector('.request-ledger-head')!.children;
    expect(headings[4]).toHaveAttribute('title', 'Total input includes cached tokens');
    expect(headings[6]).toHaveAttribute('title', 'Cache-read share of total input');
    expect(headings[8].getAttribute('title')).toContain('≈ API is plan equivalent, not spend');
    expect(root.querySelectorAll('.request-timezone')).toHaveLength(1);
    expect(root.querySelector('.request-timezone')).toHaveTextContent(
      Intl.DateTimeFormat().resolvedOptions().timeZone,
    );
    const row = root.querySelector('.request-row')!;
    expect([...row.children].map((c) => c.classList[0])).toEqual([
      'request-time',
      'request-status',
      'request-model',
      'request-client',
      'request-input',
      'request-output',
      'request-cache',
      'request-duration',
      'request-price',
      'request-stamp',
      'request-compact-facts',
      'request-details',
    ]);
    expect(row.querySelector('.request-time')?.textContent).toMatch(/^\d{2}:\d{2}:\d{2}$/);
    expect(row.querySelector('.request-status')).toHaveClass('status-complete');
    expect(row.querySelector('.request-status')).toHaveTextContent(/^200$/);
    expect(row.querySelector('.request-input')).toHaveTextContent('1k');
    expect(row.querySelector('.request-input')?.getAttribute('title')).toContain(
      (1012).toLocaleString(),
    );
    expect(row.querySelector('.request-mobile-usage')).toHaveTextContent('1k→19');
    expect(row.querySelector('.request-price')).toHaveTextContent('—');
    expect(getByRole(row as HTMLElement, 'button', { name: 'POST /request-1' })).toHaveTextContent(
      '<img src=x>',
    );
    expect(row.querySelector('img')).toBeNull();
    expect(row).not.toHaveTextContent(/request-1|First byte|Sent upstream/);
  });
  it('keeps the HTTP code and interrupted outcome together in the status cell', async () => {
    const { root, requests } = setup(1);
    requests.mockResolvedValueOnce({
      requests: [{ ...record(1), outcome: 'incomplete' }],
      total: 1,
      next: 0,
      page: 1,
    });
    await ready(root);
    expect(root.querySelector('.request-status')).toHaveClass('status-warning');
    expect(root.querySelector('.request-status-code')).toHaveTextContent(/^200$/);
    expect(root.querySelector('.request-status')).toHaveTextContent('200 Interrupted');
  });
  it.each(['unknown', 'not_applicable'] as const)(
    'uses bytes for %s token evidence in the ledger but retains observed raw counters',
    async (source) => {
      const { root, requests } = setup(1);
      requests.mockResolvedValueOnce({
        requests: [
          {
            ...record(1),
            usage: { protocol: 'responses', usage: { input: 100, output: 20, source } },
          },
        ],
        total: 1,
        next: 0,
        page: 1,
      });
      await ready(root);
      expect(root.querySelector('.request-input')).toHaveTextContent(/^3 B$/);
      expect(root.querySelector('.request-output')).toHaveTextContent(/^4 B$/);
      expect(root.querySelector('.request-output')?.getAttribute('title')).toContain(
        'received from upstream',
      );
      expect(root.querySelector('.request-mobile-usage')).toHaveTextContent(/^3 B→4 B$/);
      fireEvent.click(getByRole(root, 'button', { name: 'POST /request-1' }));
      expect(detailValues(root.querySelector('.request-detail') as HTMLElement)['Output']).toBe(
        '20',
      );
    },
  );
  it('retains partial lower-bound output qualifiers in compact and mobile usage', async () => {
    const { root, requests } = setup(1);
    requests.mockResolvedValueOnce({
      requests: [
        {
          ...record(1),
          usage: { protocol: 'responses', usage: { input: 100, output: 20, source: 'partial' } },
        },
      ],
      total: 1,
      next: 0,
      page: 1,
    });
    await ready(root);
    expect(root.querySelector('.request-output')).toHaveTextContent(/^≥20$/);
    expect(root.querySelector('.request-output')?.getAttribute('title')).toContain(
      'partial lower bound',
    );
    expect(root.querySelector('.request-mobile-usage')).toHaveTextContent(/^≥100→≥20$/);
  });
  it('groups consecutive repeated errors while the pagers still count individual requests', async () => {
    const { root, requests } = setup(51);
    const errors = Array.from({ length: 25 }, (_, i) => ({
      ...record(51 - i),
      path: '/same',
      status: 429,
      outcome: 'http_error',
      started: new Date(2026, 10, 17, 12, 0, 25 - i).getTime(),
      total_ms: 25 - i,
    }));
    requests.mockResolvedValueOnce({ requests: errors, total: 51, next: 26, page: 1 });
    await ready(root);
    expect(root.querySelectorAll('.request-row')).toHaveLength(1);
    expect(pager(root).textContent).toContain('1–25 of 51 requests');
    expect(root.querySelector('.request-time')?.getAttribute('title')).toContain(
      'Latest request, not aggregate',
    );
    expect(root.querySelector('.request-duration')?.getAttribute('title')).toContain(
      'Latest request, not aggregate',
    );
    const repeat = getByRole(root, 'button', { name: 'Expand 25 repeated requests' });
    fireEvent.click(repeat);
    expect(repeat).toHaveAttribute('aria-expanded', 'true');
    const children = root.querySelector('.request-group-rows') as HTMLElement;
    expect(children.querySelectorAll('.request-row')).toHaveLength(25);
    const open = children.querySelector('button.request-open') as HTMLButtonElement;
    fireEvent.click(open);
    const heading = getByRole(children, 'heading', { name: 'Request details' });
    expect(document.activeElement).toBe(heading);
    expect(open).toHaveAttribute('aria-expanded', 'true');
    fireEvent.keyDown(heading, { key: 'Escape' });
    expect(document.activeElement).toBe(open);
    expect(open).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(open);
    fireEvent.click(repeat);
    expect(root.querySelector('.request-detail')).toBeNull();
    expect(children.childElementCount).toBe(0);
    expect(document.activeElement).toBe(repeat);
    expect(repeat).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(repeat);
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Page 2' }));
    await ready(root, 2);
    expect(root.querySelector('.request-group-rows')).toBeNull();
    expect(root.querySelectorAll('.request-row')).toHaveLength(25);
    expect(pager(root).textContent).toContain('26–50 of 51 requests');
    expect(document.activeElement).toBe(pager(root).querySelector('[role=status]'));
  });
  it('separates local dates and never collapses measured or priced errors', async () => {
    const { root, requests } = setup(4);
    const error = { ...record(1), path: '/same', status: 500, outcome: 'http_error' };
    requests.mockResolvedValueOnce({
      requests: [
        { ...error, seq: 4, started: new Date(2026, 10, 17).getTime() },
        { ...error, seq: 3, started: new Date(2026, 10, 16).getTime() },
        {
          ...error,
          seq: 2,
          started: new Date(2026, 10, 16).getTime(),
          usage: { usage: { output: 0 } },
        },
        {
          ...error,
          started: new Date(2026, 10, 16).getTime(),
          money: { billing: 'pay_as_you_go', kind: 'charged', amount_micros: 1, currency: 'USD' },
        },
      ],
      total: 4,
      next: 0,
      page: 1,
    });
    await ready(root);
    expect(root.querySelectorAll('.request-date-group')).toHaveLength(2);
    expect(root.querySelectorAll('.request-row')).toHaveLength(4);
    expect(root.querySelector('.request-repeat')).toBeNull();
  });
});

describe('reviewed request metadata regressions', () => {
  it.each(['messages', 'bedrock', 'ollama', undefined] as const)(
    'keeps %s thinking detail semantics neutral',
    (protocol) => {
      const detail = requestDetail({
        ...record(1),
        usage: { protocol, usage: { input: 1, reasoning: -1 } },
      });
      const thinking = [...detail.querySelectorAll<HTMLElement>('.request-tokens dl > div')].find(
        (tile) => tile.querySelector('dt')?.textContent === 'Thinking',
      )!;
      expect(thinking.title).toBe('Reported thinking tokens (when separately available)');
      expect(thinking.title).not.toContain('included');
    },
  );
  it.each(['unpriced', 'unmeasured', 'estimated_floor'] as const)(
    'restores concise %s price reasons without boilerplate',
    (kind) => {
      const detail = requestDetail({
        ...record(1),
        money: {
          billing: 'plan',
          kind: 'included',
          api_equivalent: {
            kind,
            reason:
              'Counterfactual API estimate only; not a bill, spending, or subscription savings. Exact rate unavailable.',
          },
        },
      });
      expect(detailValues(detail)['Price reason']).toBe('Exact rate unavailable.');
      expect(detail).not.toHaveTextContent('Counterfactual');
      expect(detail.querySelector('details')).toBeNull();
    },
  );
  it('labels a WS turn capture as connection-scoped without tutorial prose', () => {
    const capture = document.createElement('div');
    capture.textContent = 'Capture access';
    const detail = renderRequestDetail(
      { ...record(1), websocket_connection_id: 'connection' },
      { toast: vi.fn() },
      capture,
    );
    expect(detailValues(detail)['Capture scope']).toBe('WebSocket connection');
    expect(detail).toHaveTextContent('Capture access');
  });
  it('copies latest-not-aggregate titles and aria into responsive error facts', async () => {
    const { root, requests } = setup(2);
    const error = {
      ...record(2),
      status: 400,
      outcome: 'http_error',
      path: '/same',
      request_meta: { state: 'observed' as const, model: 'model-a' },
    };
    requests.mockResolvedValueOnce({
      requests: [error, { ...error, id: 'older', seq: 1, started: Number(error.started) - 1000 }],
      next: 0,
      total: 2,
      page: 1,
    });
    await ready(root);
    await waitFor(() => expect(root.querySelectorAll('.request-row')).toHaveLength(1));
    for (const selector of [
      '.request-time',
      '.request-duration',
      '.request-stamp-time',
      '.request-compact-time',
      '.request-compact-duration',
    ]) {
      const cell = root.querySelector(selector)!;
      expect(cell.getAttribute('title')).toContain('Latest request, not aggregate');
      expect(cell.getAttribute('aria-label')).toBe(cell.getAttribute('title'));
    }
  });
});
