import { withSingletonProviders } from './test-fixtures';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  getByText,
  queryByLabelText,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyAPIError,
  ProxyClient,
  type ProxyAccount,
  type ProxyAdminRoute,
  type ProxyPreset,
  type ProxyState,
} from '../src/api/proxy-client';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import { mountProxy } from './app';
import { providerItem } from './provider-test-helpers';
import { providerOnboarding, providerRouteAction, clearNativeSetup } from './provider-onboarding';
import type { UIContext } from './dom';
import {
  agyProvider,
  claudeProvider,
  nativeLauncher,
  nativeProviderYAML,
  routeNativeProvider,
} from './native';

const account: ProxyAccount = {
  name: 'native-slot',
  kind: 'claude-bin',
  auth_mode: 'shared_vault',
  state: 'needs_login',
  generation: 1,
  updated: 1,
};
const route: ProxyAdminRoute = {
  serves: [],
  profile: '',
  provider: 'my-manual-connection',
  name: 'my-manual-connection',
  account: account.name,
  local_prefix: '/native/',
  upstream: 'https://api.anthropic.com',
  enabled: true,
  allow_insecure_http: false,
  caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
  set_headers: {},
  billing: 'plan',
};
const other: ProxyAdminRoute = {
  ...route,
  provider: 'openai',
  profile: 'openai',
  serves: [],
  name: 'openai',
  account: undefined,
  local_prefix: '/openai/',
  upstream: 'https://api.openai.com/v1',
  billing: 'pay_as_you_go',
};
const agyAccount: ProxyAccount = { ...account, name: 'personal-slot', kind: 'agy-bin' };
const agyRoute: ProxyAdminRoute = {
  ...route,
  provider: 'personal-route',
  profile: '',
  serves: [],
  name: 'personal-route',
  local_prefix: '/agy-bin/',
  upstream: 'https://daily-cloudcode-pa.googleapis.com',
  account: agyAccount.name,
};
const agyPreset: ProxyPreset = {
  family: 'inference',
  id: 'agy-bin',
  label: 'Antigravity',
  account_kind: 'agy-bin',

  upstream: agyRoute.upstream,
  auth: agyRoute.caller_auth,
};
const presets: ProxyPreset[] = [
  {
    family: 'inference',
    id: 'claude-bin',
    label: 'Claude Code',
    account_kind: 'claude-bin' as const,
    upstream: route.upstream,
    auth: route.caller_auth,
  },
  {
    family: 'inference',
    id: 'openai',
    label: 'OpenAI',
    upstream: other.upstream,
    auth: other.caller_auth,
  },
];
function snapshot(overrides: Partial<ProxyState> = {}): ProxyState {
  return {
    defaults: null,
    providers: [],
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: [],
    accounts: [],
    callers: [],
    admin_keys: [],
    secrets: [],
    presets,
    csrf: 'csrf',
    ...overrides,
  };
}
const caller = {
  id: 'legacy-client',
  name: 'Legacy Claude client',
  created: 1,
  grants: [route.name],
  revoked: false,
  created_by: '',
  external_id: '',
};
const configured = { accounts: [account], routes: [route], callers: [caller] };
const disposals: (() => void)[] = [];
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
beforeEach(() => window.history.replaceState(null, '', '/_proxy/?tab=routes'));
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  document.body.replaceChildren();
  vi.restoreAllMocks();
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor);
  else Reflect.deleteProperty(navigator, 'clipboard');
});
function setup(initial = snapshot()) {
  let current = initial;
  let serial = 0;
  const api = new ProxyClient(vi.fn());
  vi.spyOn(api, 'authStatus').mockResolvedValue({ auth: initial.auth, csrf: 'csrf' } as never);
  vi.spyOn(api, 'state').mockImplementation(async () => withSingletonProviders(current));
  vi.spyOn(api, 'beginAssertion').mockResolvedValue({
    publicKey: { challenge: 'fixture' },
  } as never);
  vi.spyOn(api, 'finishPasskey').mockResolvedValue();
  vi.spyOn(api, 'setupNative').mockImplementation(async () => {
    current = { ...current, ...configured, revision: current.revision + 1 };
    return {
      revision: current.revision,
      url: 'https://proxy.example/native/',
      token: `scoped-canary-${++serial}`,
      caller_id: `caller-${serial}`,
    };
  });
  vi.spyOn(api, 'clearNative').mockImplementation(async () => {
    current = {
      ...current,
      accounts: (current.accounts ?? []).filter((a) => a.kind !== 'claude-bin'),
      routes: current.routes.filter((r) => routeNativeProvider(r, current) !== claudeProvider),
      revision: current.revision + 1,
    };
  });
  const platform: PasskeyPlatform = {
    available: () => true,
    get: vi.fn().mockResolvedValue({ id: 'assertion' }),
    create: vi.fn(),
  };
  const root = document.createElement('div');
  document.body.append(root);
  disposals.push(mountProxy(root, api, platform));
  return { api, root, platform };
}
async function click(root: HTMLElement, name: string) {
  fireEvent.click(await waitFor(() => getByRole(root, 'button', { name })));
}
async function chooseSignInClient(root: HTMLElement, client: string | { name: string }) {
  const select = await waitFor(() => getByLabelText(root, 'Sign in from client'));
  if (typeof client === 'string') {
    fireEvent.change(select, { target: { value: client } });
  } else {
    fireEvent.change(select, { target: { value: 'new' } });
    fireEvent.input(getByLabelText(root, 'New client name'), { target: { value: client.name } });
  }
  await click(root, 'Get sign-in command');
}
function providerFixture(state = snapshot(), refresh = vi.fn().mockResolvedValue(state)) {
  const api = new ProxyClient(vi.fn());
  const errors: unknown[] = [];
  let protectedToken = false;
  const context: UIContext = {
    refresh,
    toast: vi.fn(),
    announce: vi.fn(),
    navigate: vi.fn(),
    protectToken: (value) => {
      protectedToken = value;
    },
    hasProtectedToken: () => protectedToken,
    async run(action) {
      try {
        await action();
        return true;
      } catch (error) {
        errors.push(error);
        return false;
      }
    },
  };
  const connection = {
    revision: 8,
    url: 'https://proxy.example/native/',
    token: 'scoped-canary-1',
    caller_id: 'device',
  };
  vi.spyOn(api, 'setupNative').mockResolvedValue(connection);
  vi.spyOn(api, 'clearNative').mockResolvedValue();
  return { api, context, errors, refresh, connection };
}

it('keeps the operator launcher token out of browser storage and clears it only after dismissal refresh succeeds', async () => {
  const fixture = providerFixture();
  const { api, context, errors, refresh } = fixture;
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
  const storageWrite = vi.spyOn(Storage.prototype, 'setItem');
  const view = providerOnboarding(presets[0], snapshot(), api, context, vi.fn())!;
  document.body.append(view);
  await click(view, 'Create shared route & sign in');
  await chooseSignInClient(view, { name: 'laptop' });
  const command = await waitFor(
    () => getByLabelText(view, 'Operator sign-in command') as HTMLTextAreaElement,
  );
  expect(command.value).toContain('scoped-canary-1');
  expect(command).toHaveFocus();
  expect(api.setupNative).toHaveBeenCalledExactlyOnceWith('claude', 7, undefined, 'create', {
    name: 'laptop',
  });
  expect(context.hasProtectedToken!()).toBe(true);
  expect(storageWrite).not.toHaveBeenCalled();
  await click(view, 'Copy sign-in command');
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(command.value));
  refresh.mockRejectedValueOnce(new ProxyAPIError(503, 'State unavailable.'));
  await click(view, 'I saved it — dismiss');
  await waitFor(() => expect(errors).toHaveLength(1));
  expect(context.hasProtectedToken!()).toBe(true);
  expect(command.value).toContain('scoped-canary-1');
  await click(view, 'I saved it — dismiss');
  await waitFor(() => expect(command.value).toBe(''));
  expect(context.hasProtectedToken!()).toBe(false);
});

it.each([
  [account, route],
  [agyAccount, agyRoute],
])('does not offer a new launcher on legacy $kind routes', (account, route) => {
  const state = snapshot({
    accounts: [{ ...account, auth_mode: 'vault', state: 'native_login' }],
    routes: [route],
  });
  const { api, context } = providerFixture(state);
  const view = providerRouteAction(route, state, api, context)!;
  expect(view).toHaveTextContent('must be migrated to a shared subscription');
  expect(view.querySelector('summary, button')).toBeNull();

  expect(api.setupNative).not.toHaveBeenCalled();
});

function sharedSnapshot(state: string, enabled = true): ProxyState {
  return snapshot({
    ...configured,
    accounts: [{ ...account, auth_mode: 'shared_vault', state }],
    routes: [{ ...route, enabled }],
    providers: [
      {
        id: route.provider,
        label: 'Claude Code',
        family: 'inference',
        routes: [route.name],
        status: state === 'ready' ? 'green' : 'red',
        reason: state === 'ready' ? '' : 'Sign-in needed',
      },
    ],
  });
}

it('offers only shared Claude setup and presents an operator-only launcher', async () => {
  const { root, api } = setup();
  await click(root, 'Add provider');
  expect(getByText(root, 'Sign in once, then add devices')).toBeVisible();
  await click(root, 'Claude Code');
  expect(queryByRole(root, 'button', { name: 'Create route & get launcher' })).toBeNull();
  expect(getByRole(root, 'button', { name: 'Create shared route & sign in' })).toBeVisible();
  expect(api.setupNative).not.toHaveBeenCalled();
  await click(root, 'Create shared route & sign in');
  await chooseSignInClient(root, { name: 'laptop' });
  const command = await waitFor(
    () => getByLabelText(root, 'Operator sign-in command') as HTMLTextAreaElement,
  );
  expect(api.setupNative).toHaveBeenCalledExactlyOnceWith('claude', 7, undefined, 'create', {
    name: 'laptop',
  });
  expect(command.value).toBe(
    "term-llm proxy run claude --url 'https://proxy.example/native/' --token 'scoped-canary-1'",
  );
  expect(command).toHaveFocus();
  expect(root).toHaveTextContent('Start sign-in within 15 minutes.');
  expect(queryByLabelText(root, 'Provider YAML')).toBeNull();

  vi.mocked(api.state).mockResolvedValueOnce(sharedSnapshot('needs_login'));
  await click(root, 'I saved it — dismiss');
  await waitFor(() => expect(root).toHaveTextContent('Sign-in needed'));
  // The new provider is selected and its Access tab shows the shared sign-in.
  const detail = getByRole(root, 'region', { name: `${route.provider} (Claude Code)` });
  expect(getByRole(detail, 'tab', { selected: true })).toHaveTextContent('Access');
  // The attention mark points at the tab holding the fix, not Connection.
  expect(getByRole(detail, 'tab', { name: /^Access \d+, needs attention$/ })).toBeVisible();
  expect(getByRole(detail, 'tab', { name: 'Connection' })).toBeVisible();
  expect(getByRole(detail, 'button', { name: 'Sign in to shared subscription' })).toBeVisible();
  expect(queryByRole(root, 'button', { name: 'Add device' })).toBeNull();
  expect(detail.querySelectorAll('details')).toHaveLength(0);

  vi.mocked(api.state).mockResolvedValueOnce(sharedSnapshot('ready'));
  await click(root, 'Refresh state');
  await waitFor(() =>
    expect(
      getByRole(getByRole(root, 'region', { name: `${route.provider} (Claude Code)` }), 'img', {
        name: 'Status: green',
      }),
    ).toBeVisible(),
  );
  expect(getByRole(root, 'button', { name: 'Add device' })).toBeInTheDocument();
});

it.each([
  ['needs_login', 'Sign in to shared subscription'],
  ['refreshing', 'Reconnect'],
  ['ready', 'Reconnect'],
])(
  'uses shared account state %s to show operator and worker actions directly',
  (state, operatorLabel) => {
    const snapshot = sharedSnapshot(state);
    const { api, context } = providerFixture(snapshot);
    const action = providerRouteAction(route, snapshot, api, context)!;
    document.body.append(action);

    expect(action.querySelector('details')).toBeNull();
    expect(getByRole(action, 'button', { name: operatorLabel })).toBeInTheDocument();
    expect(queryByRole(action, 'button', { name: 'Add device' }) !== null).toBe(state === 'ready');
    expect(
      providerRouteAction({ ...route, enabled: false }, snapshot, api, context),
    ).toBeUndefined();
    expect(providerRouteAction(route, { ...snapshot, mode: 'file' }, api, context)).toBeUndefined();
  },
);

it('separates reconnect from Add device on the same ready shared route', async () => {
  const state = sharedSnapshot('ready');
  const { api, context, connection, errors } = providerFixture(state);
  const view = providerRouteAction(route, state, api, context)!;
  document.body.append(view);
  await click(view, 'Reconnect');
  await click(view, 'Get sign-in command');
  await waitFor(() => expect(errors).toHaveLength(1));
  expect(api.setupNative).not.toHaveBeenCalled();
  // An armed existing client keeps its token, so the reply carries none.
  vi.mocked(api.setupNative).mockResolvedValueOnce({ ...connection, token: undefined });
  await chooseSignInClient(view, caller.id);
  const command = await waitFor(() => getByLabelText(view, 'Operator sign-in command'));
  expect(command).toHaveValue(
    'term-llm proxy run claude --url \'https://proxy.example/native/\' --token "$TERM_LLM_PROXY_TOKEN"',
  );
  expect(view).toHaveTextContent(`Run this command on the device that uses ${caller.name}`);
  expect(api.setupNative).toHaveBeenCalledExactlyOnceWith('claude', 7, route.name, 'connect', {
    caller: caller.id,
  });
  expect(context.hasProtectedToken!()).toBe(false);
  expect(queryByRole(view, 'button', { name: 'Add device' })).toBeNull();
  await click(view, 'Done');
  await waitFor(() => expect(getByRole(view, 'button', { name: 'Add device' })).toBeVisible());
  await click(view, 'Add device');
  await waitFor(() => expect(getByLabelText(view, 'Launcher command')).toBeVisible());
  expect(api.setupNative).toHaveBeenNthCalledWith(2, 'claude', 7, route.name);
  expect(view).toHaveTextContent('with an updated term-llm');
  expect(view).toHaveTextContent('does not open a browser');
  expect(queryByLabelText(view, 'Operator sign-in command')).toBeNull();
});

it('keeps clearing all Claude setup separate and requires confirmation', async () => {
  const state = snapshot(configured);
  const { api, context, refresh } = providerFixture(state);
  const view = clearNativeSetup(claudeProvider, state, api, context);
  document.body.append(view);
  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
  fireEvent.click(view);
  expect(api.clearNative).not.toHaveBeenCalled();
  fireEvent.click(view);
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
  expect(confirm).toHaveBeenCalledWith(
    expect.stringContaining('Other providers and your normal Claude setup are unchanged'),
  );
  expect(api.clearNative).toHaveBeenCalledExactlyOnceWith('claude', 7);
});

it.each(['claude', 'agy'] as const)(
  'uses the revisioned %s endpoint with CSRF and no URL tokens',
  async (launcher) => {
    const connection = {
      revision: 8,
      url: 'https://proxy.example/native/',
      token: 'scoped-canary',
      caller_id: 'caller',
    };
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ csrf: 'csrf' })))
      .mockResolvedValueOnce(new Response(JSON.stringify(connection)))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = new ProxyClient(fetcher);
    await api.login('admin');
    expect(await api.setupNative(launcher, 7, route.name)).toEqual(connection);
    await api.clearNative(launcher, 8);
    expect(fetcher.mock.calls.slice(1)).toEqual([
      [
        `/_proxy/api/${launcher}/setup`,
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ revision: 7, route: route.name }),
          headers: expect.objectContaining({ 'X-CSRF-Token': 'csrf' }),
          credentials: 'same-origin',
          cache: 'no-store',
        }),
      ],
      [
        `/_proxy/api/${launcher}/setup`,
        expect.objectContaining({
          method: 'DELETE',
          body: JSON.stringify({ revision: 8 }),
          headers: expect.objectContaining({ 'X-CSRF-Token': 'csrf' }),
        }),
      ],
    ]);
  },
);

it.each(['claude', 'agy'] as const)(
  'sends shared_action only for %s operator setup',
  async (launcher) => {
    const connection = {
      revision: 8,
      url: 'https://proxy.example/native/',
      token: 'scoped',
      caller_id: 'id',
    };
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => Response.json(connection));
    const api = new ProxyClient(fetcher);
    await api.setupNative(launcher, 7, undefined, 'create', { name: 'laptop' });
    await api.setupNative(launcher, 8, route.name, 'connect', { caller: 'c_dev' });
    await api.setupNative(launcher, 9, route.name);
    expect(
      fetcher.mock.calls.map(([url, request]) => [
        url,
        request?.method,
        JSON.parse(String(request?.body)),
      ]),
    ).toEqual([
      [
        `/_proxy/api/${launcher}/setup`,
        'POST',
        { revision: 7, shared_action: 'create', name: 'laptop' },
      ],
      [
        `/_proxy/api/${launcher}/setup`,
        'POST',
        { revision: 8, route: route.name, shared_action: 'connect', caller: 'c_dev' },
      ],
      [`/_proxy/api/${launcher}/setup`, 'POST', { revision: 9, route: route.name }],
    ]);
  },
);

it('safely quotes native command arguments and literal YAML, without API-key examples', () => {
  expect(
    nativeLauncher(
      claudeProvider,
      "https://proxy.test/o'connor/",
      "scope'$x;echo unsafe",
      'shared-worker',
    ),
  ).toBe(
    "term-llm proxy run claude --url 'https://proxy.test/o'\\''connor/' --token 'scope'\\''$x;echo unsafe'",
  );
  expect(nativeProviderYAML(claudeProvider, "https://proxy.test/o'connor/", "scope'token")).toBe(
    "providers:\n  claude-bin:\n    proxy:\n      url: 'https://proxy.test/o''connor/'\n      token: 'scope''token'\n",
  );
  expect(routeNativeProvider({ ...other, name: 'claude' }, snapshot(configured))).toBeUndefined();
});

const clientProviders: ProxyState['providers'] = [
  {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'claude-plan',
    label: 'Claude Code',
    routes: [route.name],
  },
  {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'openai-api',
    label: 'OpenAI',
    routes: [other.name],
  },
];

it('uses a native Claude launcher and curl examples for mixed provider approvals', async () => {
  const { root, api } = setup(
    snapshot({ ...sharedSnapshot('ready'), routes: [route, other], providers: clientProviders }),
  );
  const create = vi.spyOn(api, 'createProviderCaller').mockResolvedValue({
    token: 'generic-scoped-canary',
    caller: {
      id: 'mixed',
      name: 'mixed',
      created: 1,
      grants: [route.name, other.name],
      revoked: false,
      created_by: '',
      external_id: '',
    },
  });
  await click(root, 'Clients');
  await click(root, 'New client');
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'mixed' } });
  fireEvent.click(getByRole(root, 'checkbox', { name: 'claude-plan' }));
  fireEvent.click(getByRole(root, 'checkbox', { name: 'openai-api' }));
  await click(root, 'Create client');
  expect(create).toHaveBeenCalledExactlyOnceWith('mixed', ['claude-plan', 'openai-api'], 7);
  await waitFor(() => expect(getByLabelText(root, 'Launcher command')).toBeVisible());
  expect((getByLabelText(root, 'Launcher command') as HTMLTextAreaElement).value).toContain(
    "--token 'generic-scoped-canary'",
  );
  expect([...root.querySelectorAll('pre:not(:has(code))')].map((pre) => pre.textContent)).toEqual([
    expect.stringContaining('/openai/'),
  ]);
  expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('curl -H');
  expect(root.querySelector('pre code')?.textContent).toBe(
    `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
  );
  expect(root.textContent).toContain(
    'For curl examples, set TOKEN in your shell to this client token, not the administrator token.',
  );
});

it.each(
  [
    { account, route, label: 'Claude Code', launcher: 'claude' },
    { account: agyAccount, route: agyRoute, label: 'Antigravity', launcher: 'agy' },
  ].flatMap((provider) =>
    ['legacy', 'needs_login', 'refreshing', 'ready', 'disabled'].map((state) => ({
      ...provider,
      state,
    })),
  ),
)(
  'only approves $label for client launchers after operator sign-in ($state)',
  async ({ account, route, label, launcher, state }) => {
    const ready = state === 'ready';
    const { root, api } = setup(
      snapshot({
        accounts: [
          {
            ...account,
            auth_mode: state === 'legacy' ? 'vault' : 'shared_vault',
            state: state === 'legacy' ? 'native_login' : state === 'disabled' ? 'ready' : state,
          },
        ],
        routes: [{ ...route, enabled: state !== 'disabled' }, other],
        providers: [
          {
            family: 'inference',
            status: 'green',
            reason: '',
            id: 'native',
            label,
            routes: [route.name],
          },
          clientProviders[1],
        ],
        callers: [caller],
      }),
    );
    const create = vi.spyOn(api, 'createProviderCaller').mockResolvedValue({
      token: 'shared-worker-canary',
      caller: {
        ...caller,
        id: 'new-client',
        name: 'Worker',
        grants: [ready ? route.name : other.name],
      },
    });
    await click(root, 'Clients');
    await click(root, 'New client');
    const native = getByRole(root, 'checkbox', {
      name: ready ? 'native' : 'native (shared subscription not ready)',
    });
    const openAI = getByRole(root, 'checkbox', { name: 'openai-api' });
    expect(openAI).toBeEnabled();
    if (ready) expect(native).toBeEnabled();
    else expect(native).toBeDisabled();

    fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Worker' } });
    fireEvent.click(ready ? native : openAI);
    await click(root, 'Create client');
    expect(create).toHaveBeenCalledExactlyOnceWith('Worker', [ready ? 'native' : 'openai-api'], 7);
    await waitFor(() => expect(getByLabelText(root, 'Client token')).toBeVisible());
    expect(queryByLabelText(root, 'Operator sign-in command')).toBeNull();
    if (ready) {
      expect(getByLabelText(root, 'Launcher command')).toHaveValue(
        `term-llm proxy run ${launcher} --url '${location.origin + route.local_prefix}' --token 'shared-worker-canary'`,
      );
      expect(root).toHaveTextContent('does not open a browser');
    } else {
      expect(queryByLabelText(root, 'Launcher command')).toBeNull();
      expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('/openai/');
    }
  },
);

it('describes removing a shared grant without signing the subscription out', async () => {
  const { root } = setup(sharedSnapshot('ready'));
  await click(root, 'Providers');
  fireEvent.click(await waitFor(() => providerItem(root, route.name)));
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  await click(root, `Remove ${caller.name}’s access to ${route.provider}`);
  expect(confirm).toHaveBeenCalledWith(
    expect.stringContaining('The shared Claude sign-in and other clients’ access are unchanged.'),
  );
});

it('creates a shared Antigravity route and shows an operator-only login command', async () => {
  const { root, api } = setup(snapshot({ presets: [agyPreset] }));
  await click(root, 'Add provider');
  expect(getByRole(root, 'button', { name: 'Antigravity' })).toHaveTextContent(
    'Personal subscriptions only',
  );
  await click(root, 'Antigravity');
  expect(root).toHaveTextContent('Personal subscriptions only, not Enterprise.');
  expect(root).toHaveTextContent('macOS credential isolation has not been verified');
  expect(queryByRole(root, 'button', { name: 'Connect new account' })).toBeNull();
  expect(queryByRole(root, 'button', { name: 'Create route & get launcher' })).toBeNull();
  await click(root, 'Create shared route & sign in');
  await chooseSignInClient(root, { name: 'laptop' });
  const command = await waitFor(() => getByLabelText(root, 'Operator sign-in command'));
  expect(api.setupNative).toHaveBeenCalledExactlyOnceWith('agy', 7, undefined, 'create', {
    name: 'laptop',
  });
  expect(command).toHaveValue(
    "term-llm proxy run agy --url 'https://proxy.example/native/' --token 'scoped-canary-1' -- acp --login",
  );
  expect(queryByLabelText(root, 'Provider YAML')).toBeNull();
});

it('signs in and adds workers to the same shared Antigravity route without sending workers through login', async () => {
  const shared: ProxyAccount = { ...agyAccount, auth_mode: 'shared_vault', state: 'needs_login' };
  const state = snapshot({ accounts: [account, shared], routes: [route, agyRoute] });
  const { api, context, refresh } = providerFixture(state);
  const pending = providerRouteAction(agyRoute, state, api, context)!;
  document.body.append(pending);

  expect(pending.querySelector('details')).toBeNull();
  expect(queryByRole(pending, 'button', { name: 'Add device' })).toBeNull();
  await click(pending, 'Sign in to shared subscription');
  await chooseSignInClient(pending, { name: 'laptop' });
  await waitFor(() =>
    expect(api.setupNative).toHaveBeenCalledExactlyOnceWith('agy', 7, agyRoute.name, 'connect', {
      name: 'laptop',
    }),
  );
  expect(getByLabelText(pending, 'Operator sign-in command')).toHaveValue(
    "term-llm proxy run agy --url 'https://proxy.example/native/' --token 'scoped-canary-1' -- acp --login",
  );
  await click(pending, 'I saved it — dismiss');
  await waitFor(() => expect(context.hasProtectedToken!()).toBe(false));
  expect(refresh).toHaveBeenCalledWith(`route:${agyRoute.name}`);
  pending.remove();

  const ready = { ...state, accounts: [account, { ...shared, state: 'ready' }], revision: 8 };

  const view = providerRouteAction(agyRoute, ready, api, context)!;
  document.body.append(view);
  await click(view, 'Add device');
  expect(api.setupNative).toHaveBeenNthCalledWith(2, 'agy', 8, agyRoute.name);
  expect(getByLabelText(view, 'Launcher command')).toHaveValue(
    "term-llm proxy run agy --url 'https://proxy.example/native/' --token 'scoped-canary-1'",
  );
  expect(getByLabelText(view, 'Provider YAML')).toHaveValue(
    "providers:\n  agy-bin:\n    proxy:\n      url: 'https://proxy.example/native/'\n      token: 'scoped-canary-1'\n",
  );
  expect(view.querySelector('details')).toBeNull();
  await click(view, 'I saved it — dismiss');
  await waitFor(() => expect(context.hasProtectedToken!()).toBe(false));
  const clear = clearNativeSetup(agyProvider, ready, api, context);
  document.body.append(clear);
  expect(clear).toHaveAccessibleName('Clear all Antigravity setup');
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(clear);
  await waitFor(() => expect(api.clearNative).toHaveBeenCalledExactlyOnceWith('agy', 8));
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Other providers'));
});
