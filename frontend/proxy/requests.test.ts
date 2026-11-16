import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, getByLabelText, getByRole, waitFor } from '@testing-library/preact';
import { ProxyClient, type ProxyRequest, type ProxyState } from '../src/api/proxy-client';
import { mountProxy } from './app';
import { requestPageNumbers } from './requests';

const snapshot: ProxyState = {
  auth: 'token',
  mode: 'ui',
  revision: 1,
  routes: [],
  callers: [],
  secrets: [],
  csrf: 'test',
};
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
  return { root, requests };
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
  vi.restoreAllMocks();
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
  it('jumps directly to the last page and replaces rows rather than appending', async () => {
    const { root, requests } = setup();
    await ready(root);
    fireEvent.click(getByRole(root, 'button', { name: 'POST /request-2225' }));
    expect(root.querySelector('.request-detail')).not.toBeNull();
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Page 89' }));
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
  it('resets to the first page when filters are applied and keeps filters on later pages', async () => {
    const { root, requests } = setup();
    await ready(root);
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Page 2' }));
    await ready(root, 2);
    const filters = root.querySelector('details.request-filters') as HTMLDetailsElement;
    filters.open = true;
    fireEvent.input(getByLabelText(root, 'Method'), { target: { value: 'get' } });
    fireEvent.click(getByRole(root, 'button', { name: 'Refresh results' }));
    await ready(root);
    expect(requests).toHaveBeenLastCalledWith(
      expect.objectContaining({ method: 'GET', page: '1' }),
    );
    fireEvent.click(getByRole(pager(root), 'button', { name: 'Next' }));
    await ready(root, 2);
    expect(requests).toHaveBeenLastCalledWith(
      expect.objectContaining({ method: 'GET', page: '2' }),
    );
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
    complete({ requests: [record(2200)], next: 0, page: 2, page_size: 25, total: 26 });
    await ready(root, 2);
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
