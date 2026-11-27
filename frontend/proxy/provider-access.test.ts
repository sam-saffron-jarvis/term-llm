import { afterEach, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByLabelText,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyAPIError,
  ProxyClient,
  type ProxyAccount,
  type ProxyCaller,
  type ProxyAdminRoute,
  type ProxyRouteRule,
  type ProxyState,
} from '../src/api/proxy-client';
import type { UIContext } from './dom';
import { providerAccess } from './provider-access';
import type { ProviderGroup } from './provider-groups';

const auth = { header: 'Authorization', prefix: 'Bearer ' };
const route = (name: string, upstream: string): ProxyAdminRoute => ({
  serves: [],
  profile: '',
  provider: name,
  name,
  local_prefix: `/${name}/`,
  upstream,
  enabled: true,
  allow_insecure_http: false,
  caller_auth: auth,
  set_headers: {},
  billing: 'pay_as_you_go',
});

const openaiRoute = route('openai', 'https://api.openai.com/v1');
const chatgptRoute: ProxyAdminRoute = {
  ...route('chatgpt', 'https://chatgpt.com/backend-api/codex'),
  account: 'subscription',
};
const chatgptBackendRoute: ProxyAdminRoute = {
  ...route('chatgpt-backend', 'https://chatgpt.com/backend-api'),
  account: 'subscription',
};
const chatgptMediaRoute: ProxyAdminRoute = {
  ...route('chatgpt-openai', 'https://api.openai.com/v1'),
  account: 'subscription',
};
const claudeAccount: ProxyAccount = {
  name: 'claude-slot',
  kind: 'claude-bin',
  state: 'ready',
  auth_mode: 'shared_vault',
  generation: 1,
  updated: 1,
};
const claudeRoute: ProxyAdminRoute = {
  ...route('claude', 'https://api.anthropic.com'),
  account: claudeAccount.name,
};
const rules: ProxyRouteRule[] = [
  {
    path_prefix: '/',
    upstream: 'https://opencode.ai/zen/go/v1',
    caller_auth: auth,
    set_headers: {},
  },
  {
    path_prefix: '/messages/',
    upstream: 'https://opencode.ai/zen/go',
    caller_auth: { header: 'x-api-key', prefix: '' },
    set_headers: {},
  },
];
const unifiedRoute: ProxyAdminRoute = {
  ...route('opencode', ''),
  caller_auth: { header: '', prefix: '' },
  rules,
};

const openaiGroup: ProviderGroup = {
  family: 'inference',
  status: 'green',
  reason: '',
  id: 'openai',
  label: 'OpenAI',
  routes: [openaiRoute],
};
const chatgptGroup: ProviderGroup = {
  family: 'inference',
  status: 'green',
  reason: '',
  id: 'chatgpt',
  label: 'ChatGPT',
  routes: [chatgptRoute, chatgptBackendRoute, chatgptMediaRoute],
};
const claudeGroup: ProviderGroup = {
  family: 'inference',
  status: 'green',
  reason: '',
  id: 'claude',
  label: 'Claude Code',
  routes: [claudeRoute],
};

function caller(overrides: Partial<ProxyCaller> = {}): ProxyCaller {
  return {
    id: 'laptop',
    name: 'Laptop',
    created: 1,
    revoked: false,
    grants: [openaiRoute.name],
    created_by: '',
    external_id: '',
    ...overrides,
  };
}

function snapshot(group: ProviderGroup, overrides: Partial<ProxyState> = {}): ProxyState {
  return {
    defaults: null,
    providers: [],
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: group.routes,
    secrets: [],
    callers: [],
    admin_keys: [],
    accounts: [],
    csrf: 'csrf',
    ...overrides,
  };
}

const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor);
  else Reflect.deleteProperty(navigator, 'clipboard');
});

function fixture(
  group: ProviderGroup,
  overrides: Partial<ProxyState> = {},
  contextOverrides: Partial<UIContext> = {},
) {
  const state = snapshot(group, overrides);
  const api = new ProxyClient(vi.fn());
  const createProviderCaller = vi.spyOn(api, 'createProviderCaller');
  const updateProviderApprovals = vi.spyOn(api, 'updateProviderApprovals').mockResolvedValue();
  const updateCaller = vi.spyOn(api, 'updateCaller').mockResolvedValue();
  const createCaller = vi.spyOn(api, 'createCaller').mockResolvedValue({
    token: 'unused',
    caller: caller(),
  });
  const announce = vi.fn();
  const protectToken = vi.fn();
  const refresh = vi.fn(async () => state as ProxyState | undefined);
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
    refresh,
    toast: vi.fn(),
    announce,
    navigate: vi.fn(),
    protectToken,
    hasProtectedToken: () => false,
    ...contextOverrides,
  };
  const root = providerAccess(group, state, api, context);
  document.body.append(root);
  return {
    api,
    root,
    context,
    announce,
    refresh,
    protectToken,
    createProviderCaller,
    updateProviderApprovals,
    updateCaller,
    createCaller,
  };
}

it('renders client approval controls directly without an extra expand toggle', () => {
  const { root } = fixture(openaiGroup);
  expect(root.querySelector('details')).toBeNull();
  expect(root.querySelector<HTMLElement>('.route-access-panel')!.hidden).toBe(false);
  expect(root).toHaveTextContent('No clients approved yet.');
  expect(root).not.toHaveTextContent('Add existing client');
  expect(queryByLabelText(root, 'Client')).toBeNull();
  expect(getByLabelText(root, 'Client name')).toBeVisible();
});

it('lists approved clients once and removes the whole provider approval without touching their token', async () => {
  const active = caller({ id: 'lap', name: 'Laptop', grants: [openaiRoute.name, 'docs'] });
  const revoked = caller({ id: 'old', name: 'Old laptop', revoked: true });
  const { root, updateProviderApprovals, updateCaller, refresh, announce } = fixture(openaiGroup, {
    callers: [active, revoked],
  });
  expect(root.querySelectorAll('.route-access-client')).toHaveLength(1);
  expect(root.querySelector('.route-access-client')).toHaveTextContent('Laptop');

  const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
  const remove = getByRole(root, 'button', { name: /Remove Laptop’s access to openai/ });
  fireEvent.click(remove);
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  fireEvent.click(remove);
  await waitFor(() =>
    expect(updateProviderApprovals).toHaveBeenCalledWith(
      'lap',
      [{ id: 'openai', approved: false }],
      7,
    ),
  );
  expect(confirm).toHaveBeenLastCalledWith(
    expect.stringContaining('Access to other providers and its token stay unchanged.'),
  );
  await waitFor(() => expect(refresh).toHaveBeenCalledWith('route:openai'));
  expect(announce).toHaveBeenLastCalledWith('Removed Laptop’s access to openai.');
  expect(updateCaller).not.toHaveBeenCalled();
  // The revoked client is neither counted nor offered another approval.
  expect(root).not.toHaveTextContent('Old laptop');
});

it('approves every route of a provider for an existing client in one explicit write', async () => {
  const docs = caller({ id: 'docs', name: 'Docs app', grants: ['docs'] });
  const revoked = caller({ id: 'retired', name: 'Retired', revoked: true, grants: [] });
  const { root, createProviderCaller, updateProviderApprovals, refresh, announce } = fixture(
    chatgptGroup,
    { callers: [docs, revoked] },
  );
  const docsChoice = getByLabelText(root, 'Docs app') as HTMLInputElement;
  expect(root.querySelectorAll('.route-access-grant input[type="checkbox"]')).toHaveLength(1);
  expect(docsChoice.checked).toBe(false);
  fireEvent.click(getByRole(root, 'button', { name: 'Approve selected' }));
  await waitFor(() =>
    expect(announce).toHaveBeenCalledWith('Select at least one client to approve.'),
  );
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  fireEvent.click(docsChoice);
  fireEvent.click(getByRole(root, 'button', { name: 'Approve selected' }));
  await waitFor(() =>
    expect(updateProviderApprovals).toHaveBeenCalledWith(
      'docs',
      [{ id: 'chatgpt', approved: true }],
      7,
    ),
  );
  expect(refresh).toHaveBeenCalledWith('route:chatgpt');
  expect(announce).toHaveBeenLastCalledWith(
    'Approved Docs app for chatgpt. Its token and other approvals are unchanged.',
  );
  // Approval covers the provider; it never reissues or rewrites the client.
  expect(createProviderCaller).not.toHaveBeenCalled();
});

it('approves several clients in one submit, re-reading the revision between writes', async () => {
  const phone = caller({ id: 'phone', name: 'Phone', grants: [] });
  const docs = caller({ id: 'docs', name: 'Docs app', grants: [] });
  const { root, updateProviderApprovals, refresh, announce } = fixture(openaiGroup, {
    callers: [phone, docs],
  });
  refresh.mockResolvedValueOnce(snapshot(openaiGroup, { revision: 8, callers: [phone, docs] }));
  fireEvent.click(getByLabelText(root, 'Phone'));
  fireEvent.click(getByLabelText(root, 'Docs app'));
  fireEvent.click(getByRole(root, 'button', { name: 'Approve selected' }));
  await waitFor(() => expect(updateProviderApprovals).toHaveBeenCalledTimes(2));
  expect(updateProviderApprovals.mock.calls.map(([id, , revision]) => [id, revision])).toEqual([
    ['phone', 7],
    ['docs', 8],
  ]);
  expect(refresh.mock.calls).toEqual([[undefined, true], ['route:openai']]);
  expect(announce).toHaveBeenLastCalledWith(
    'Approved Phone, Docs app for openai. Their tokens and other approvals are unchanged.',
  );
});

it('keeps client creation behind New client once clients exist', () => {
  const { root } = fixture(openaiGroup, { callers: [caller()] });
  const toggle = getByRole(root, 'button', { name: 'New client' });
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  expect(queryByLabelText(root, 'Client name')).not.toBeVisible();
  fireEvent.click(toggle);
  expect(getByLabelText(root, 'Client name')).toBeVisible();
  expect(getByLabelText(root, 'Client name')).toHaveFocus();
  fireEvent.click(getByRole(root, 'button', { name: 'Cancel' }));
  expect(queryByLabelText(root, 'Client name')).not.toBeVisible();
  expect(getByRole(root, 'button', { name: 'New client' })).toHaveFocus();
});

it('shows legacy partial approval as needing approval and covers all routes only on an explicit click', async () => {
  const laptop = caller({ id: 'lap', name: 'Laptop', grants: ['chatgpt'] });
  const { root, updateProviderApprovals, refresh, announce } = fixture(chatgptGroup, {
    callers: [laptop],
  });
  expect(root.querySelector('.route-access-client')).toHaveTextContent('Laptop');
  expect(root.querySelector('.route-access-client')).toHaveTextContent('Needs approval');
  const choice = getByLabelText(root, 'Laptop (needs approval)') as HTMLInputElement;
  // Existing route grants stay exactly as they are until the operator covers
  // the whole provider explicitly.
  expect(choice.checked).toBe(false);
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  fireEvent.click(choice);
  fireEvent.click(getByRole(root, 'button', { name: 'Approve selected' }));
  await waitFor(() =>
    expect(updateProviderApprovals).toHaveBeenCalledWith(
      'lap',
      [{ id: 'chatgpt', approved: true }],
      7,
    ),
  );
  expect(refresh).toHaveBeenCalledWith('route:chatgpt');
  expect(announce).toHaveBeenLastCalledWith(
    'Approved Laptop for chatgpt. Its token and other approvals are unchanged.',
  );
});

it('creates a provider-scoped client, shows the token once, and guards dismissal', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
  const storageWrite = vi.spyOn(Storage.prototype, 'setItem');
  const issued = caller({ id: 'phone', name: 'Phone', grants: [openaiRoute.name] });
  const { root, createProviderCaller, updateProviderApprovals, refresh, protectToken, announce } =
    fixture(openaiGroup);
  createProviderCaller.mockResolvedValue({ token: 'sk-tlp-once-only', caller: issued });
  // The first refresh is the preserveView reconciliation; it must not replace
  // the one-time token with a re-rendered view.
  refresh.mockResolvedValueOnce(snapshot(openaiGroup, { callers: [issued] }));
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Phone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));

  const token = await waitFor(() => getByLabelText(root, 'Client token') as HTMLInputElement);
  expect(createProviderCaller).toHaveBeenCalledExactlyOnceWith('Phone', ['openai'], 7);
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  expect(token.value).toBe('sk-tlp-once-only');
  expect(token.readOnly).toBe(true);
  expect(token.className).toBe('caller-token');
  expect(token).toHaveFocus();
  expect(protectToken.mock.calls).toEqual([[true, true], [true]]);
  // The secret is never repeated in guidance text, markup, or browser storage.
  expect(root.textContent).not.toContain('sk-tlp-once-only');
  expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('$TOKEN');
  expect(root).toHaveTextContent(`Base URL: ${location.origin}/openai/`);
  expect(root).toHaveTextContent('set TOKEN in your shell to this client token');
  expect(root).toHaveTextContent('Do not set a global HTTP_PROXY or HTTPS_PROXY');
  expect(root).toHaveTextContent('Access is limited to the current routes for openai.');
  expect(storageWrite).not.toHaveBeenCalled();
  expect(announce).toHaveBeenLastCalledWith(expect.stringContaining('Copy the token now'));

  fireEvent.click(getByRole(root, 'button', { name: 'Copy token' }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('sk-tlp-once-only'));

  fireEvent.click(getByRole(root, 'button', { name: 'Copy YAML' }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith(
      `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
    ),
  );

  fireEvent.click(getByRole(root, 'button', { name: 'Copy with token' }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith(
      `proxy:\n  url: ${location.origin}\n  token: 'sk-tlp-once-only'`,
    ),
  );

  // A successful reconciliation must not replace the only copy of the token.
  await waitFor(() => expect(refresh).toHaveBeenCalledWith(undefined, true));
  expect(token.value).toBe('sk-tlp-once-only');

  fireEvent.click(getByRole(root, 'button', { name: 'I saved it — dismiss' }));
  // Focus returns to the provider, which is what stays on screen.
  await waitFor(() => expect(refresh).toHaveBeenCalledWith('route:openai'));
  expect(token.value).toBe('');
  expect(protectToken).toHaveBeenLastCalledWith(false);
  expect(root).toHaveTextContent('Phone');
});

it('creates one provider-scoped client whose details cover every route in the group', async () => {
  const issued = caller({ id: 'phone', name: 'Phone', grants: ['chatgpt'] });
  const { root, createProviderCaller, updateProviderApprovals } = fixture(chatgptGroup);
  createProviderCaller.mockResolvedValue({ token: 'sk-tlp-chatgpt', caller: issued });
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Phone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));
  await waitFor(() => getByLabelText(root, 'Client token'));
  expect(createProviderCaller).toHaveBeenCalledExactlyOnceWith('Phone', ['chatgpt'], 7);
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  expect(root).toHaveTextContent(`Base URL: ${location.origin}/chatgpt/`);
  expect(root).toHaveTextContent(`Base URL: ${location.origin}/chatgpt-backend/`);
  expect(root).toHaveTextContent(`Base URL: ${location.origin}/chatgpt-openai/`);
  expect(root).toHaveTextContent('Access is limited to the current routes for chatgpt.');
});

async function issueProviderToken(
  selected: ProxyAdminRoute,
  label = selected.name,
): Promise<HTMLElement> {
  const group: ProviderGroup = {
    family: 'inference',
    status: 'green',
    reason: '',
    id: selected.name,
    label,
    routes: [selected],
  };
  const { root, createProviderCaller } = fixture(group);
  createProviderCaller.mockResolvedValue({
    token: 'sk-tlp-route-scoped',
    caller: caller({ id: 'phone', name: 'Phone', grants: [selected.name] }),
  });
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Phone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));
  await waitFor(() => getByLabelText(root, 'Client token'));
  return root;
}

it.each([
  [chatgptRoute, 'ChatGPT'],
  [route('venice', 'https://api.venice.ai/api/v1'), 'Venice'],
] as const)(
  'shows safe term-llm YAML alongside curl for canonical $name',
  async (selected, label) => {
    const root = await issueProviderToken(selected, label);
    expect(getByRole(root, 'heading', { name: 'term-llm client configuration' })).toBeVisible();
    expect(root.querySelector('pre code')?.textContent).toBe(
      `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
    );
    expect(root).toHaveTextContent('unapproved integrations may be unavailable');
    expect(root).toHaveTextContent(`Base URL: ${location.origin}${selected.local_prefix}`);
    expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('$TOKEN');
    expect(root.textContent).not.toContain('sk-tlp-route-scoped');
    expect(root).toHaveTextContent(`Access is limited to the current routes for ${selected.name}.`);
  },
);

it.each([
  { ...chatgptRoute, name: 'family', local_prefix: '/family/' },
  { ...chatgptRoute, local_prefix: '/custom/' },
  { ...chatgptRoute, upstream: 'https://custom.example/codex' },
])('uses stored IDs even for noncanonical $name ($local_prefix → $upstream)', async (selected) => {
  const root = await issueProviderToken(selected);
  expect(root.querySelector('pre code')?.textContent).not.toContain('providers:');
  expect(root).toHaveTextContent(`keep it and add ${selected.name}`);
  expect(root).toHaveTextContent(`Base URL: ${location.origin}${selected.local_prefix}`);
  expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('$TOKEN');
});

it('shows every subpath header and one shared token for a unified route', async () => {
  const group: ProviderGroup = {
    family: 'inference',
    status: 'green',
    reason: '',
    id: 'opencode',
    label: 'opencode',
    routes: [unifiedRoute],
  };
  const { root, createProviderCaller } = fixture(group);
  createProviderCaller.mockResolvedValue({
    token: 'sk-tlp-unified',
    caller: caller({ id: 'phone', name: 'Phone', grants: ['opencode'] }),
  });
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Phone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));
  await waitFor(() => expect(getByLabelText(root, 'Client token')).toBeTruthy());
  expect(createProviderCaller).toHaveBeenCalledExactlyOnceWith('Phone', ['opencode'], 7);
  expect(root).toHaveTextContent('/opencode/messages/ · client x-api-key');
  expect(root).toHaveTextContent('one client token and one grant cover every subpath');
  expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent(
    `${location.origin}/opencode/`,
  );
  expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('$TOKEN');
});

it('points native Claude providers at the device launcher instead of a generic client form', () => {
  const state = {
    accounts: [claudeAccount],
    callers: [caller({ id: 'mac', name: 'Mac', grants: ['claude'] })],
  };
  const { root, createProviderCaller } = fixture(claudeGroup, state);
  expect(root).toHaveTextContent(
    'Claude Code uses a device launcher instead of a generic API URL. Use Add device under Shared subscription to connect a new device.',
  );
  expect(queryByLabelText(root, 'Client name')).toBeNull();
  expect(queryByRole(root, 'button', { name: 'Create client' })).toBeNull();
  expect(queryByLabelText(root, 'Client')).toBeNull();
  expect(root.querySelector('pre:not(:has(code))')).toBeNull();
  expect(root).not.toHaveTextContent(`Base URL: ${location.origin}`);
  // Existing devices stay visible and revocable without touching other approvals.
  expect(getByRole(root, 'button', { name: /Remove Mac’s access to claude/ })).toBeVisible();
  expect(createProviderCaller).not.toHaveBeenCalled();
});

it('explains file-managed providers without offering generic connection details', () => {
  const { root } = fixture(openaiGroup, { mode: 'file' });
  expect(root).toHaveTextContent(
    'Routes are file-managed. Approvals are editable, but a configuration reload may drop incompatible grants.',
  );
  expect(root.querySelector('pre:not(:has(code))')).toBeNull();
});

it('warns that removing shared Claude access leaves the subscription and other clients intact', async () => {
  const { root, updateProviderApprovals, updateCaller, refresh } = fixture(claudeGroup, {
    accounts: [claudeAccount],
    callers: [caller({ id: 'mac', name: 'Mac', grants: ['claude'] })],
  });
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(getByRole(root, 'button', { name: /Remove Mac’s access to claude/ }));
  await waitFor(() =>
    expect(updateProviderApprovals).toHaveBeenCalledWith(
      'mac',
      [{ id: 'claude', approved: false }],
      7,
    ),
  );
  expect(confirm).toHaveBeenLastCalledWith(
    expect.stringContaining('The shared Claude sign-in and other clients’ access are unchanged.'),
  );
  await waitFor(() => expect(refresh).toHaveBeenCalledWith('route:claude'));
  expect(updateCaller).not.toHaveBeenCalled();
});

it('refuses creation, provider approval, and removal while another token is on screen', async () => {
  const { root, createProviderCaller, updateProviderApprovals, refresh, announce } = fixture(
    openaiGroup,
    {
      callers: [
        caller({ id: 'lap', name: 'Laptop' }),
        caller({ id: 'docs', name: 'Docs app', grants: ['docs'] }),
      ],
    },
    { hasProtectedToken: () => true },
  );
  fireEvent.click(getByRole(root, 'button', { name: 'New client' }));
  fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Phone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));
  expect(createProviderCaller).not.toHaveBeenCalled();
  expect(announce).toHaveBeenCalledWith(
    'Save or dismiss the open one-time client token before creating another client.',
  );

  fireEvent.click(getByLabelText(root, 'Docs app'));
  fireEvent.click(getByRole(root, 'button', { name: 'Approve selected' }));
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  expect(announce).toHaveBeenLastCalledWith(
    'Save or dismiss the open one-time client token before approving provider access.',
  );

  vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(getByRole(root, 'button', { name: /Remove Laptop’s access to openai/ }));
  expect(updateProviderApprovals).not.toHaveBeenCalled();
  expect(announce).toHaveBeenLastCalledWith(
    'Save or dismiss the open one-time client token before removing access.',
  );
  expect(refresh).not.toHaveBeenCalled();
});

it('writes provider approvals in passkey mode without a second passkey prompt', async () => {
  const { api, root, updateProviderApprovals, updateCaller } = fixture(openaiGroup, {
    auth: 'passkey',
    callers: [caller({ grants: ['openai', 'docs'] })],
  });
  const begin = vi.spyOn(api, 'beginAssertion');
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(getByRole(root, 'button', { name: /Remove Laptop’s access to openai/ }));
  await waitFor(() =>
    expect(updateProviderApprovals).toHaveBeenCalledWith(
      'laptop',
      [{ id: 'openai', approved: false }],
      7,
    ),
  );
  expect(begin).not.toHaveBeenCalled();
  expect(updateCaller).not.toHaveBeenCalled();
});

it('refreshes provider state without replaying a stale approval write', async () => {
  const { root, updateProviderApprovals, refresh, announce } = fixture(openaiGroup, {
    callers: [caller()],
  });
  updateProviderApprovals.mockRejectedValueOnce(
    new ProxyAPIError(409, 'Provider configuration changed; refresh before changing access'),
  );
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(getByRole(root, 'button', { name: /Remove Laptop’s access to openai/ }));
  await waitFor(() => expect(refresh).toHaveBeenCalledWith('route:openai'));
  expect(updateProviderApprovals).toHaveBeenCalledOnce();
  expect(announce).toHaveBeenCalledWith(
    'Provider configuration changed; refresh before changing access',
  );
});

it('puts approval first and lists approved clients by recent use with relative times', () => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date(2026, 10, 26, 12, 0));
  try {
    const now = Date.now();
    const { root } = fixture(chatgptGroup, {
      callers: [
        caller({
          id: 'c_old00000',
          name: 'Old',
          grants: ['chatgpt', 'chatgpt-backend', 'chatgpt-openai'],
          last_used: now - 3 * 86_400_000,
        }),
        caller({
          id: 'c_dupe1111',
          name: 'Dupe',
          grants: ['chatgpt', 'chatgpt-backend', 'chatgpt-openai'],
          last_used: now - 5 * 60_000,
        }),
        caller({
          id: 'c_dupe2222',
          name: 'Dupe',
          grants: ['chatgpt'],
          last_used: now - 2 * 3_600_000,
        }),
        caller({ id: 'c_waiting0', name: 'Waiting', grants: [] }),
      ],
    });
    const panel = root.querySelector<HTMLElement>('.route-access-panel')!;
    // Adding is the action, so it sits above a list that can grow long.
    expect(panel.firstElementChild).toHaveClass('route-access-add');
    expect(getByRole(panel, 'group', { name: 'Approve clients' })).toBeVisible();
    expect(getByRole(panel, 'heading', { name: 'Clients with access 3' })).toBeVisible();
    const rows = [...panel.querySelectorAll<HTMLElement>('.route-access-client')];
    expect(rows.map((row) => row.querySelector('strong')!.textContent)).toEqual([
      'Dupe #dupe1',
      'Dupe #dupe2',
      'Old',
    ]);
    expect(rows[0]).toHaveTextContent('Last seen 5 min ago');
    expect(rows[1]).toHaveTextContent('Last seen 2 hr ago');
    expect(rows[1].querySelector('.route-access-pending')).toHaveTextContent('Needs approval');
    expect(rows[2]).toHaveTextContent('Last seen 3 days ago');
    expect(rows[2].querySelector('small')).toHaveAttribute(
      'title',
      new Date(now - 3 * 86_400_000).toLocaleString(),
    );
    // Duplicate names keep their suffix in the action, too.
    expect(
      getByRole(rows[0], 'button', { name: 'Remove Dupe #dupe1’s access to chatgpt' }),
    ).toBeVisible();
    const remove = getByRole(rows[2], 'button', { name: 'Remove Old’s access to chatgpt' });
    expect(remove).toHaveTextContent(/^Remove$/);
    expect(remove).toHaveClass('route-access-remove');
    expect(panel).toHaveTextContent('Approval covers every current route of this provider.');
  } finally {
    vi.useRealTimers();
  }
});
