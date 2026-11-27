import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyClient,
  type ProxyAdminKey,
  type ProxyCaller,
  type ProxyAdminRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import { duplicateSuffixes, lastSeen, shortDate, sortCallers } from './client-list';
import type { UIContext } from './dom';
import { callersView } from './security';

const route = (name: string): ProxyAdminRoute => ({
  serves: [],
  profile: '',
  provider: name,
  name,
  local_prefix: `/${name}/`,
  upstream: 'https://example.com/',
  enabled: true,
  allow_insecure_http: false,
  caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
  set_headers: {},
});
const caller = (overrides: Partial<ProxyCaller>): ProxyCaller => ({
  id: 'c_one',
  name: 'One',
  created: 1,
  revoked: false,
  grants: [],
  created_by: '',
  external_id: '',
  ...overrides,
});

afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

function setup(
  callers: ProxyCaller[],
  adminKeys: ProxyAdminKey[] = [],
  overrides: Partial<ProxyState> = {},
) {
  const state: ProxyState = {
    defaults: null,
    providers: [],
    auth: 'token',
    mode: 'ui',
    revision: 3,
    routes: [route('venice'), route('chatgpt')],
    secrets: [],
    callers,
    admin_keys: adminKeys,
    csrf: 'csrf',
    ...overrides,
  };
  const context: UIContext = {
    run: async (action) => {
      await action();
      return true;
    },
    refresh: vi.fn(async () => state),
    toast: vi.fn(),
    announce: vi.fn(),
    navigate: vi.fn(),
    protectToken: vi.fn(),
  };
  const fetcher = vi.fn<typeof fetch>(async () => Response.json({ ok: true }));
  const root = callersView(state, new ProxyClient(fetcher), context);
  document.body.append(root);
  return { root, fetcher, context };
}

function render(callers: ProxyCaller[], adminKeys: ProxyAdminKey[] = []) {
  return setup(callers, adminKeys).root;
}

describe('shortDate', () => {
  const now = new Date(2026, 10, 18, 16, 0);
  it.each([
    [undefined, 'Never'],
    ['nonsense', 'Unknown'],
    [new Date(2026, 10, 18, 9, 5).getTime(), /^Today /],
    [new Date(2026, 10, 17, 23, 59).getTime(), /^Yesterday /],
    [new Date(2026, 10, 2, 12).getTime(), /^Nov 2$/],
    [new Date(2025, 10, 2, 12).getTime(), /2025/],
  ])('formats %s', (value, expected) => {
    expect(shortDate(value, 'Never', now)).toMatch(expected);
  });
});

describe('lastSeen', () => {
  const now = new Date(2026, 10, 18, 16, 0);
  const ago = (minutes: number) => now.getTime() - minutes * 60_000;
  it.each([
    [undefined, 'never'],
    ['nonsense', 'unknown'],
    [ago(0.5), 'just now'],
    [ago(-5), 'just now'],
    [ago(1), '1 min ago'],
    [ago(59), '59 min ago'],
    [ago(60), '1 hr ago'],
    [ago(23 * 60 + 59), '23 hr ago'],
    [ago(24 * 60), 'yesterday'],
    [ago(2 * 24 * 60), '2 days ago'],
    [ago(6 * 24 * 60), '6 days ago'],
    [new Date(2026, 10, 2, 12).getTime(), 'Nov 2'],
  ])('describes %s as %s', (value, expected) => {
    expect(lastSeen(value, now)).toBe(expected);
  });
});

it('suffixes only duplicate names, lengthening IDs until they differ', () => {
  const suffixes = duplicateSuffixes([
    caller({ id: 'c_abcd1111', name: 'Claude Code' }),
    caller({ id: 'c_abcd2222', name: 'Claude Code' }),
    caller({ id: 'c_ffff0000', name: 'arch' }),
  ]);
  expect(Object.fromEntries(suffixes)).toEqual({ c_abcd1111: 'abcd1', c_abcd2222: 'abcd2' });
});

it('sorts by most recent use, then newest creation', () => {
  const sorted = sortCallers([
    caller({ id: 'old', created: 1 }),
    caller({ id: 'used', created: 1, last_used: 5 }),
    caller({ id: 'new', created: 2 }),
  ]);
  expect(sorted.map((c) => c.id)).toEqual(['used', 'new', 'old']);
});

it('collapses creation behind New client once active clients exist', () => {
  const root = render([caller({ grants: ['venice'] })]);
  const name = getByLabelText(root, 'Client name');
  const toggle = getByRole(root, 'button', { name: 'New client' });
  expect(name).not.toBeVisible();
  fireEvent.click(toggle);
  expect(toggle).toHaveAttribute('aria-expanded', 'true');
  expect(document.activeElement).toBe(name);
  fireEvent.input(name, { target: { value: 'draft' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Cancel' }));
  expect(name).not.toBeVisible();
  expect((name as HTMLInputElement).value).toBe('');
  expect(document.activeElement).toBe(toggle);
});

it('opens creation by default when no active client exists', () => {
  const root = render([caller({ revoked: true })]);
  expect(getByLabelText(root, 'Client name')).toBeVisible();
});

it('labels API-created clients with their key name or revoked status and external ID', () => {
  const root = render(
    [
      caller({ id: 'c_ci', name: 'CI', created_by: 'ak_active', external_id: 'build/runner-1' }),
      caller({ id: 'c_old', name: 'Old runner', created_by: 'ak_deleted', revoked: true }),
      caller({ id: 'c_browser', name: 'Browser' }),
    ],
    [{ id: 'ak_active', name: '<Automation>', created: 1, last_used: 0 }],
  );
  const active = getByRole(root, 'button', { name: 'CI' }).closest('article');
  expect(active).toHaveTextContent('Created by API key ‹<Automation>›');
  expect(active).toHaveTextContent('External ID: build/runner-1');
  expect(active?.querySelector('automation')).toBeNull();
  const revoked = root.querySelector('details.client-revoked')!;
  revoked.setAttribute('open', '');
  expect(
    getByRole(revoked as HTMLElement, 'heading', { name: 'Old runner' }).closest('article'),
  ).toHaveTextContent('Created by a revoked API key');
  const browser = getByRole(root, 'button', { name: 'Browser' }).closest('article');
  expect(browser).not.toHaveTextContent(/Created by|External ID/);
});

it('names providers by ID when several share a label', () => {
  const provider = (id: string) => ({
    family: 'inference' as const,
    status: 'green' as const,
    reason: '',
    id,
    label: 'Anthropic',
    routes: [id],
  });
  const root = setup([caller({ id: 'c_work', name: 'Work', grants: ['anthropic-work'] })], [], {
    routes: [route('anthropic'), route('anthropic-work')],
    providers: [provider('anthropic'), provider('anthropic-work')],
  }).root;
  const grants = root.querySelector('.client-grants')!;
  expect(grants).toHaveAccessibleName('Providers: anthropic-work');
  expect(grants).not.toHaveTextContent('Anthropic');

  fireEvent.click(getByRole(root, 'button', { name: 'Work' }));
  const personal = getByRole(root, 'checkbox', { name: 'anthropic' });
  const work = getByRole(root, 'checkbox', { name: 'anthropic-work' });
  expect(personal).not.toBeChecked();
  expect(work).toBeChecked();
  // The shared label stays available on hover.
  expect(work.closest('label')).toHaveAttribute(
    'title',
    'anthropic-work (Anthropic). Routes: anthropic-work',
  );
});

it('keeps one active row expanded and folds revoked clients away by default', () => {
  const root = render([
    caller({ id: 'c_aaaa1', name: 'Claude Code', grants: ['venice'] }),
    caller({ id: 'c_bbbb2', name: 'Claude Code', grants: ['chatgpt'] }),
    caller({ id: 'c_old', name: 'term', revoked: true, grants: ['venice'] }),
  ]);
  const first = getByRole(root, 'button', { name: 'Claude Code #aaaa' });
  const second = getByRole(root, 'button', { name: 'Claude Code #bbbb' });
  fireEvent.click(first);
  expect(root.querySelectorAll('.client-detail form')).toHaveLength(1);
  fireEvent.click(second);
  expect(first).toHaveAttribute('aria-expanded', 'false');
  expect(second).toHaveAttribute('aria-expanded', 'true');
  expect(root.querySelectorAll('.client-detail form')).toHaveLength(1);
  expect(getByRole(root, 'button', { name: 'Revoke Claude Code #bbbb' })).toBeVisible();
  const revoked = root.querySelector('details.client-revoked')!;
  expect(revoked.querySelector('summary')).toHaveTextContent('Revoked (1)');
  expect(revoked).toHaveTextContent('term');
  expect(revoked).not.toHaveAttribute('open');
});

it.each([
  {
    control: 'Delete Archived #old1',
    confirmation: 'Permanently delete Archived #old1? Request history is kept.',
    body: { id: 'c_old1', purge: true },
    announcement: 'Deleted Archived #old1. Request history is kept.',
  },
  {
    control: 'Delete all revoked',
    confirmation: 'Permanently delete all revoked clients? Request history is kept.',
    body: { purge_revoked: true },
    announcement: 'Deleted all revoked clients. Request history is kept.',
  },
])('confirms $control before deleting and refreshing', async (test) => {
  const { root, fetcher, context } = setup([
    caller({ name: 'Active' }),
    caller({ id: 'c_old1', name: 'Archived', revoked: true }),
    caller({ id: 'c_old2', name: 'Archived', revoked: true }),
  ]);
  expect(queryByRole(root, 'button', { name: 'Delete Active' })).toBeNull();
  const revoked = root.querySelector<HTMLDetailsElement>('details.client-revoked')!;
  revoked.open = true;
  const remove = getByRole(revoked, 'button', { name: test.control });
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
  fireEvent.click(remove);
  expect(confirm).toHaveBeenCalledWith(test.confirmation);
  expect(fetcher).not.toHaveBeenCalled();
  expect(context.refresh).not.toHaveBeenCalled();

  fireEvent.click(remove);
  await waitFor(() => expect(context.announce).toHaveBeenCalledWith(test.announcement));
  expect(fetcher).toHaveBeenCalledExactlyOnceWith(
    '/_proxy/api/callers',
    expect.objectContaining({ method: 'DELETE', body: JSON.stringify(test.body) }),
  );
  expect(context.refresh).toHaveBeenCalledTimes(1);
});
