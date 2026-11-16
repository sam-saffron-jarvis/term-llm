import { afterEach, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByLabelText,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyClient,
  type ProxyAccount,
  type ProxyPreset,
  type ProxyRoute,
  type ProxyState,
} from '../src/api/proxy-client';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import type { UIContext } from './dom';
import { routesView } from './routes';

const presets: ProxyPreset[] = [
  {
    id: 'openai',
    label: 'OpenAI API',
    upstream: 'https://api.openai.com/v1',
    auth: { header: 'Authorization', prefix: 'Bearer ' },
    billing: 'pay_as_you_go',
    usage: 'auto',
    websocket: true,
  },
  {
    id: 'chatgpt',
    label: 'ChatGPT plan',
    upstream: 'https://chatgpt.com/backend-api/codex',
    auth: { header: 'Authorization', prefix: 'Bearer ' },
    billing: 'plan',
    usage: 'auto',
    websocket: true,
    account_kind: 'chatgpt',
  },
  {
    id: 'jina',
    label: 'Jina embeddings',
    upstream: 'https://api.jina.ai/v1',
    auth: { header: 'Authorization', prefix: 'Bearer ' },
    billing: 'pay_as_you_go',
  },
];
const accounts: ProxyAccount[] = [
  { name: 'home-chat', kind: 'chatgpt', state: 'ready', generation: 1, updated: 1 },
  { name: 'home-grok', kind: 'grok', state: 'ready', generation: 1, updated: 1 },
];
function setup(overrides: Partial<ProxyState> = {}) {
  const api = new ProxyClient(vi.fn());
  const save = vi.spyOn(api, 'config').mockResolvedValue();
  const announce = vi.fn();
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
    announce,
    refresh: vi.fn(),
    protectToken: vi.fn(),
    navigate: vi.fn(),
  };
  const platform: PasskeyPlatform = { available: () => true, create: vi.fn(), get: vi.fn() };
  const state: ProxyState = {
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: [],
    secrets: [],
    callers: [],
    csrf: 'csrf-token',
    presets,
    accounts,
    ...overrides,
  };
  const root = routesView(state, api, context, platform);
  document.body.append(root);
  return { root, save, announce };
}
function choose(root: HTMLElement, id: string) {
  fireEvent.click(getByRole(root, 'button', { name: 'Add route' }));
  fireEvent.change(getByLabelText(root, 'Provider'), { target: { value: id } });
}
afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

it('creates an account-kind route from an explicitly chosen same-kind account, with no static secret', async () => {
  const { root, save } = setup();
  choose(root, 'chatgpt');
  expect(root.querySelector('.header-substitution')).toBeNull();
  const account = getByLabelText(root, 'Trusted account') as HTMLSelectElement;
  expect([...account.options].map((o) => o.value)).toEqual(['', 'home-chat']);
  expect(account).toHaveValue('');
  expect(account.required).toBe(true);
  expect(getByLabelText(root, 'How do you pay for this provider?')).toHaveValue('plan');
  fireEvent.change(account, { target: { value: 'home-chat' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][1]).toEqual([
    {
      name: 'chatgpt',
      local_prefix: '/chatgpt/',
      upstream: 'https://chatgpt.com/backend-api/codex',
      enabled: true,
      allow_insecure_http: false,
      caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
      set_headers: {},
      billing: 'plan',
      account: 'home-chat',
      websocket: true,
      usage: 'auto',
    },
  ]);
});

it('refuses to save an account-kind route without a valid same-kind account', async () => {
  const { root, save, announce } = setup();
  choose(root, 'chatgpt');
  const form = root.querySelector<HTMLFormElement>('.route-form')!;
  // fireEvent.submit bypasses native "required" validation, as a scripted submit would.
  fireEvent.submit(form);
  await waitFor(() => expect(announce).toHaveBeenCalledWith(expect.stringContaining('chatgpt')));
  // A forged option of another kind is rejected too.
  const account = getByLabelText(root, 'Trusted account') as HTMLSelectElement;
  const forged = document.createElement('option');
  forged.value = 'home-grok';
  account.append(forged);
  fireEvent.change(account, { target: { value: 'home-grok' } });
  fireEvent.submit(form);
  await waitFor(() => expect(announce).toHaveBeenCalledTimes(2));
  expect(save).not.toHaveBeenCalled();
});

it('points to Accounts when no account of the required kind exists', () => {
  const { root } = setup({ accounts: [accounts[1]] });
  choose(root, 'chatgpt');
  expect((getByLabelText(root, 'Trusted account') as HTMLSelectElement).options).toHaveLength(1);
  expect(root.querySelector('.route-form')?.textContent).toContain(
    'No chatgpt account exists yet. Add one under Accounts below',
  );
});

it('applies profile billing, usage and WebSocket defaults to API-key routes only when present', async () => {
  const { root, save } = setup({
    secrets: [
      { name: 'openai-key', origin: 'https://api.openai.com', source: 'stored' },
      { name: 'jina-key', origin: 'https://api.jina.ai', source: 'stored' },
    ],
  });
  choose(root, 'openai');
  expect(queryByLabelText(root, 'Trusted account')).toBeNull();
  expect(getByLabelText(root, 'Forward WebSocket upgrades')).toBeChecked();
  expect(getByLabelText(root, 'Usage measurement')).toHaveValue('auto');
  fireEvent.change(getByLabelText(root, 'Secret name'), { target: { value: 'openai-key' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][1][0]).toMatchObject({
    billing: 'pay_as_you_go',
    websocket: true,
    usage: 'auto',
    set_headers: { Authorization: { secret: 'openai-key', prefix: 'Bearer ' } },
  });
  expect(save.mock.calls[0][1][0]).not.toHaveProperty('account');

  vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.change(getByLabelText(root, 'Provider'), { target: { value: 'jina' } });
  expect(getByLabelText(root, 'Forward WebSocket upgrades')).not.toBeChecked();
  expect(getByLabelText(root, 'Usage measurement')).toHaveValue('off');
  fireEvent.change(getByLabelText(root, 'Secret name'), { target: { value: 'jina-key' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  const jina = save.mock.calls[1][1][0];
  expect(jina.websocket).toBeUndefined();
  expect(jina.usage).toBeUndefined();
});

it('preserves an existing route’s account, transport and admission policy when unrelated fields change', async () => {
  const existing: ProxyRoute = {
    name: 'plan',
    local_prefix: '/plan/',
    upstream: 'https://chatgpt.com/backend-api/codex',
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
    billing: 'plan',
    account: 'retired',
    websocket: true,
    usage: 'off',
    allow: ['POST /responses'],
    redirects: 'refuse',
  };
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
  // The saved account no longer exists: it must stay selectable and unchanged.
  const { root, save } = setup({ routes: [existing] });
  fireEvent.click(getByRole(root, 'button', { name: 'Edit plan' }));
  expect(getByLabelText(root, 'Trusted account')).toHaveValue('retired');
  expect(getByLabelText(root, 'Forward WebSocket upgrades')).toBeChecked();
  expect(getByLabelText(root, 'Usage measurement')).toHaveValue('off');
  fireEvent.change(getByLabelText(root, 'How do you pay for this provider?'), {
    target: { value: 'unspecified' },
  });
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][1]).toEqual([{ ...existing, billing: 'unspecified' }]);
  expect(confirm).not.toHaveBeenCalled();
});

it('asks before a transport change that drops grants on an existing route', async () => {
  const existing: ProxyRoute = {
    name: 'api',
    local_prefix: '/api/',
    upstream: 'https://api.openai.com/v1',
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  };
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  const { root, save } = setup({ routes: [existing] });
  fireEvent.click(getByRole(root, 'button', { name: 'Edit api' }));
  expect(queryByLabelText(root, 'Trusted account')).toBeNull();
  fireEvent.click(getByLabelText(root, 'Forward WebSocket upgrades'));
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(confirm).toHaveBeenCalledOnce());
  expect(save).not.toHaveBeenCalled();
});
