import { afterEach, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyClient,
  type ProxyAccount,
  type ProxyCaller,
  type ProxyProvider,
  type ProxyAdminRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import type { UIContext } from './dom';
import type { ProviderTabID } from './provider-test-helpers';
import { routesView } from './routes';

const veniceRoute: ProxyAdminRoute = {
  serves: [],
  profile: 'venice',
  provider: 'venice',
  name: 'venice',
  local_prefix: '/venice/',
  upstream: 'https://api.venice.ai/api/v1',
  enabled: true,
  allow_insecure_http: false,
  caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
  set_headers: { Authorization: { secret: 'venice-key', prefix: 'Bearer ' } },
};

const chatgptRoutes: ProxyAdminRoute[] = [
  {
    serves: [],
    profile: 'chatgpt',
    provider: 'chatgpt',
    name: 'chatgpt',
    local_prefix: '/chatgpt/',
    upstream: 'https://chatgpt.com/backend-api/codex',
    enabled: true,
    allow_insecure_http: false,
    account: 'chatgpt',
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  },
  {
    serves: [],
    profile: 'chatgpt-backend',
    provider: 'chatgpt',
    name: 'chatgpt-backend',
    local_prefix: '/chatgpt-backend/',
    upstream: 'https://chatgpt.com/backend-api',
    enabled: true,
    allow_insecure_http: false,
    account: 'chatgpt',
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  },
  {
    serves: [],
    profile: 'chatgpt-openai',
    provider: 'chatgpt',
    name: 'chatgpt-openai',
    local_prefix: '/chatgpt-openai/',
    upstream: 'https://api.openai.com/v1',
    enabled: true,
    allow_insecure_http: false,
    account: 'chatgpt',
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  },
];

const claudeRoute: ProxyAdminRoute = {
  serves: [],
  profile: 'claude-bin',
  provider: 'claude-bin',
  name: 'claude-bin',
  local_prefix: '/claude-bin/',
  upstream: 'https://api.anthropic.com',
  enabled: true,
  allow_insecure_http: false,
  account: 'claude-bin',
  caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
  set_headers: {},
};

const agyRoute: ProxyAdminRoute = {
  serves: [],
  profile: 'agy-bin',
  provider: 'agy-bin',
  name: 'agy-bin',
  local_prefix: '/agy-bin/',
  upstream: 'https://daily-cloudcode-pa.googleapis.com',
  enabled: true,
  allow_insecure_http: false,
  account: 'agy-bin',
  caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
  set_headers: {},
};

const accounts: ProxyAccount[] = [
  { name: 'chatgpt', kind: 'chatgpt', state: 'ready', generation: 1, updated: 1 },
  {
    name: 'claude-bin',
    kind: 'claude-bin',
    auth_mode: 'shared_vault',
    state: 'ready',
    generation: 1,
    updated: 1,
  },
  {
    name: 'agy-bin',
    kind: 'agy-bin',
    auth_mode: 'shared_vault',
    state: 'needs_login',
    generation: 1,
    updated: 1,
  },
];

const callers: ProxyCaller[] = [
  {
    id: 'c1',
    name: 'arch2',
    created: 1,
    revoked: false,
    grants: ['venice', 'chatgpt', 'chatgpt-backend', 'chatgpt-openai', 'claude-bin'],
    created_by: '',
    external_id: '',
  },
];

const providers: ProxyProvider[] = [
  {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'venice',
    label: 'Venice',
    routes: ['venice'],
  },
  {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'claude-bin',
    label: 'Claude Code',
    routes: ['claude-bin'],
  },
  {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'chatgpt',
    label: 'ChatGPT',
    routes: ['chatgpt', 'chatgpt-backend', 'chatgpt-openai'],
  },
];

function setup(overrides: Partial<ProxyState> = {}) {
  const api = new ProxyClient(vi.fn());
  const deleteProvider = vi.spyOn(api, 'deleteProvider').mockResolvedValue({ revision: 8 });
  const clearNative = vi.spyOn(api, 'clearNative').mockResolvedValue();
  const setupNative = vi.spyOn(api, 'setupNative').mockResolvedValue({
    revision: 8,
    url: 'https://proxy.example/claude-bin/',
    token: 'scoped-canary-worker',
    caller_id: 'w1',
  });
  const announce = vi.fn();
  const refresh = vi.fn().mockResolvedValue(undefined);
  let protectedToken = false;
  const context: UIContext = {
    run: async (action) => {
      try {
        await action();
        return true;
      } catch (error) {
        announce(error instanceof Error ? error.message : 'failed');
        return false;
      }
    },
    toast: vi.fn(),
    announce,
    refresh,
    protectToken: (shown) => {
      protectedToken = shown;
    },
    hasProtectedToken: () => protectedToken,
    navigate: vi.fn(),
  };
  const state: ProxyState = {
    defaults: null,
    auth: 'token',
    mode: 'ui',
    revision: 7,
    csrf: 'csrf',
    routes: [veniceRoute, claudeRoute, ...chatgptRoutes],
    providers,
    secrets: [{ name: 'venice-key', routes: null, source: 'stored' }],
    accounts,
    callers,
    admin_keys: [],
    ...overrides,
  };
  const root = routesView(state, api, context);
  document.body.append(root);
  return { root, api, deleteProvider, clearNative, setupNative, announce, refresh };
}

function openProvider(
  root: HTMLElement,
  label: string,
  tab: ProviderTabID = 'settings',
): HTMLElement {
  const list = root.querySelector<HTMLElement>('.provider-list')!;
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Providers are named by ID; the label follows it in parentheses.
  const title = `^[^ ]+ \\(${escaped}\\)`;
  fireEvent.click(getByRole(list, 'button', { name: new RegExp(`${title},`) }));
  const detail = getByRole(root, 'region', { name: new RegExp(`${title}$`) });
  fireEvent.click(detail.querySelector<HTMLElement>(`[role="tab"][data-tab="${tab}"]`)!);
  return detail;
}

afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

it('deletes a single-route API-key provider after confirmation and respects cancellation', async () => {
  const { root, deleteProvider, clearNative, refresh, announce } = setup();
  const manage = openProvider(root, 'Venice');
  // A one-line summary beside the button; the confirm() carries the details.
  expect(manage.querySelector('.provider-danger-zone p')).toHaveTextContent(
    'Removes 1 route and access for 1 client. Can’t be undone.',
  );
  const remove = getByRole(manage, 'button', { name: 'Delete provider venice' });
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);

  fireEvent.click(remove);
  expect(confirm).toHaveBeenCalledOnce();
  expect(deleteProvider).not.toHaveBeenCalled();

  fireEvent.click(remove);
  await waitFor(() => expect(deleteProvider).toHaveBeenCalledOnce());
  expect(confirm.mock.calls[0]?.[0]).toContain('Delete provider venice?');
  expect(confirm.mock.calls[0]?.[0]).toContain('Removes route venice');
  expect(confirm.mock.calls[0]?.[0]).toContain('for arch2');
  expect(confirm.mock.calls[0]?.[0]).toContain(
    'Keys no other route uses are removed from Secrets.',
  );
  expect(deleteProvider).toHaveBeenCalledWith(7, 'venice');
  expect(clearNative).not.toHaveBeenCalled();
  expect(refresh).toHaveBeenCalledOnce();
  expect(announce).toHaveBeenLastCalledWith(expect.stringContaining('Deleted provider venice.'));
});

it('deletes every route of a multi-route provider in one revision-checked write', async () => {
  const { root, deleteProvider, clearNative, refresh } = setup();
  const manage = openProvider(root, 'ChatGPT');
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);

  fireEvent.click(getByRole(manage, 'button', { name: 'Delete provider chatgpt' }));
  await waitFor(() => expect(deleteProvider).toHaveBeenCalledOnce());
  expect(confirm.mock.calls[0]?.[0]).toContain('Delete provider chatgpt?');
  expect(confirm.mock.calls[0]?.[0]).toContain(
    'Removes 3 routes (chatgpt, chatgpt-backend, chatgpt-openai)',
  );
  expect(confirm.mock.calls[0]?.[0]).toContain('Connected accounts stay until removed separately.');
  expect(deleteProvider).toHaveBeenCalledWith(7, 'chatgpt');
  expect(clearNative).not.toHaveBeenCalled();
  expect(refresh).toHaveBeenCalledOnce();
});

it.each([
  ['Claude Code', 'claude', 'Claude', claudeRoute, 'claude-bin'],
  ['Antigravity', 'agy', 'Antigravity', agyRoute, 'agy-bin'],
] as const)(
  'deletes native provider %s with the providers API after confirmation',
  async (label, _launcher, _shortName, route, id) => {
    const { root, deleteProvider, clearNative, refresh, announce } = setup({
      routes: [veniceRoute, route],
      providers: [
        {
          family: 'inference',
          status: 'green',
          reason: '',
          id: 'venice',
          label: 'Venice',
          routes: ['venice'],
        },
        { family: 'inference', status: 'green', reason: '', id, label, routes: [route.name] },
      ],
    });
    const manage = openProvider(root, label);
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);

    fireEvent.click(getByRole(manage, 'button', { name: `Delete provider ${id}` }));
    await waitFor(() => expect(deleteProvider).toHaveBeenCalledExactlyOnceWith(7, id));
    expect(confirm.mock.calls[0]?.[0]).toContain(`Delete provider ${id}?`);
    expect(clearNative).not.toHaveBeenCalled();
    expect(refresh).toHaveBeenCalledOnce();
    expect(announce).toHaveBeenLastCalledWith(expect.stringContaining(`Deleted provider ${id}.`));
  },
);

it('refuses provider deletion while a one-time launcher is displayed', async () => {
  const { root, clearNative, announce, deleteProvider } = setup();
  const manage = openProvider(root, 'Claude Code', 'access');
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);

  fireEvent.click(getByRole(manage, 'button', { name: 'Add device' }));
  await waitFor(() => expect(getByLabelText(manage, 'Launcher command')).toBeVisible());

  // The launcher stays on screen: Settings cannot be opened over it.
  fireEvent.click(getByRole(manage, 'tab', { name: 'Settings' }));
  expect(getByRole(manage, 'tab', { selected: true })).toHaveTextContent('Access');
  expect(getByLabelText(manage, 'Launcher command')).toBeVisible();
  expect(announce).toHaveBeenLastCalledWith(
    'Finish saving or dismissing the one-time connection details first.',
  );
  announce.mockClear();
  fireEvent.click(
    getByRole(manage, 'button', { name: 'Delete provider claude-bin', hidden: true }),
  );
  expect(confirm).not.toHaveBeenCalled();
  expect(deleteProvider).not.toHaveBeenCalled();
  expect(clearNative).not.toHaveBeenCalled();
  expect(announce).toHaveBeenLastCalledWith(
    'Finish saving or dismissing the one-time connection details first.',
  );
});

it('hides Delete provider in read-only file mode', () => {
  const { root } = setup({ mode: 'file' });
  for (const label of ['Venice', 'Claude Code', 'ChatGPT']) {
    const manage = openProvider(root, label, 'access');
    expect(queryByRole(manage, 'tab', { name: 'Settings' })).toBeNull();
    expect(queryByRole(manage, 'button', { name: /^Delete provider /, hidden: true })).toBeNull();
  }
});

it('never renders nested disclosures in a provider’s detail for any provider type', async () => {
  const { root } = setup();
  for (const label of ['Venice', 'Claude Code', 'ChatGPT']) {
    const manage = openProvider(root, label);
    expect(manage.querySelectorAll('details')).toHaveLength(0);
  }

  // Opening an inline route editor still introduces zero nested disclosures.
  const veniceManage = openProvider(root, 'Venice', 'connection');
  fireEvent.click(getByRole(veniceManage, 'button', { name: 'Edit venice' }));
  expect(veniceManage.querySelectorAll('details')).toHaveLength(0);
  expect(getByRole(veniceManage, 'button', { name: 'Delete route venice' })).toBeVisible();
  fireEvent.click(getByRole(veniceManage, 'button', { name: 'Cancel' }));

  // Issuing a worker launcher introduces zero nested disclosures.
  const claudeManage = openProvider(root, 'Claude Code', 'access');
  fireEvent.click(getByRole(claudeManage, 'button', { name: 'Add device' }));
  await waitFor(() => expect(getByLabelText(claudeManage, 'Launcher command')).toBeVisible());
  expect(claudeManage.querySelectorAll('details')).toHaveLength(0);
  const integration = claudeManage.querySelector<HTMLElement>('.native-integration')!;
  expect(getByLabelText(integration, 'Provider YAML')).toBeVisible();
});
