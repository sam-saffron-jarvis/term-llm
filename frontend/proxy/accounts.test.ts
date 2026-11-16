import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, getByLabelText, getByRole, waitFor } from '@testing-library/preact';
import { ProxyClient, type ProxyAccount, type ProxyState } from '../src/api/proxy-client';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import type { UIContext } from './dom';
import { accountsView, parseAccountCredentials } from './accounts';

const account: ProxyAccount = {
  name: 'home-chat',
  kind: 'chatgpt',
  state: 'needs_login',
  generation: 2,
  updated: 1,
};
function setup(auth: 'token' | 'passkey' = 'passkey') {
  const api = new ProxyClient(vi.fn());
  const announce = vi.fn();
  const ctx: UIContext = {
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
  const platform: PasskeyPlatform = {
    available: () => true,
    create: vi.fn(),
    get: vi.fn().mockResolvedValue({ id: 'assertion' }),
  };
  vi.spyOn(api, 'beginAssertion').mockResolvedValue({
    publicKey: { challenge: 'fixture' },
  } as never);
  vi.spyOn(api, 'finishPasskey').mockResolvedValue();
  const state = { auth, accounts: [account] } as ProxyState;
  const view = accountsView(state, api, ctx, platform);
  document.body.append(view);
  return { api, view, platform, announce };
}
afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});
it('shows safe account metadata under a collapsed disclosure, not credentials or a new tab', () => {
  const { view } = setup();
  expect(view.tagName).toBe('DETAILS');
  expect((view as HTMLDetailsElement).open).toBe(false);
  expect(view.textContent).toContain('home-chat');
  expect(view.textContent).toContain('needs login');
  expect(view.querySelector('input')).toBeNull();
});
it('clears credential paste before fresh passkey authentication and replacement', async () => {
  const { api, view } = setup();
  fireEvent.click(getByRole(view, 'button', { name: 'Replace credentials' }));
  const paste = getByLabelText(view, 'Paste credential JSON (write-only)') as HTMLInputElement;
  expect(paste.type).toBe('password');
  expect(paste.value).toBe('');
  const credentials = {
    access_token: 'canary-access',
    refresh_token: 'canary-refresh',
    account_id: 'canary-account',
    expires_at: 4102444800,
  };
  paste.value = JSON.stringify(credentials);
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  vi.mocked(api.beginAssertion).mockImplementation(async () => {
    expect(paste.value).toBe('');
    return { publicKey: { challenge: 'fixture' } } as never;
  });
  const save = vi
    .spyOn(api, 'saveAccount')
    .mockResolvedValue({ account: { ...account, state: 'ready' } });
  fireEvent.click(getByRole(view, 'button', { name: 'Replace account credentials' }));
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith({
      name: 'home-chat',
      kind: 'chatgpt',
      replace: true,
      credentials,
    }),
  );
  expect(api.beginAssertion).toHaveBeenCalledWith('reauth');
  expect(api.finishPasskey).toHaveBeenCalledBefore(save);
  await waitFor(() => expect(view.textContent).toContain('ready'));
  expect(view.textContent).not.toContain('canary-');
});
it('does not send credentials when the fresh passkey prompt is cancelled', async () => {
  const { api, view, platform } = setup();
  fireEvent.click(getByRole(view, 'button', { name: 'Replace credentials' }));
  const paste = getByLabelText(view, 'Paste credential JSON (write-only)') as HTMLInputElement;
  paste.value = '{"access_token":"canary"}';
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  vi.mocked(platform.get).mockRejectedValue(new DOMException('canary-secret', 'NotAllowedError'));
  const save = vi.spyOn(api, 'saveAccount');
  fireEvent.click(getByRole(view, 'button', { name: 'Replace account credentials' }));
  await waitFor(() => expect(platform.get).toHaveBeenCalled());
  expect(save).not.toHaveBeenCalled();
  expect(paste.value).toBe('');
});
it('uses fresh authentication for account sign-out too', async () => {
  const { api, view } = setup();
  const signOut = vi.spyOn(api, 'signOutAccount').mockResolvedValue({ account });
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  fireEvent.click(getByRole(view, 'button', { name: 'Sign out account' }));
  await waitFor(() => expect(signOut).toHaveBeenCalledWith('home-chat'));
  expect(api.finishPasskey).toHaveBeenCalledBefore(signOut);
});
it('uses real account endpoint verbs and existing CSRF transport', async () => {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(new Response(JSON.stringify({ csrf: 'csrf' })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ accounts: null })))
    .mockImplementation(async () => new Response(JSON.stringify({ account })));
  const api = new ProxyClient(fetcher);
  await api.login('admin');
  expect(await api.accounts()).toEqual([]);
  const payload = {
    name: 'test',
    kind: 'copilot' as const,
    credentials: { github_token: 'canary' },
  };
  await api.saveAccount(payload);
  await api.saveAccount({ ...payload, replace: true });
  await api.signOutAccount('test');
  expect(fetcher.mock.calls.map((call) => call[1]?.method)).toEqual([
    'POST',
    'GET',
    'POST',
    'PUT',
    'DELETE',
  ]);
  expect(fetcher.mock.calls[4]?.[1]?.headers).toEqual(
    expect.objectContaining({ 'X-CSRF-Token': 'csrf' }),
  );
});
it.each([
  '{"issuer":"https://evil.example"}',
  '{"origins":[]}',
  '{"__proto__":{}}',
  '{"access_token":"canary\\nsecret"}',
  '{"expires_at":1763200000000}',
  '[]',
  '{}',
  'canary-invalid-json',
])('rejects unsafe paste without echoing it: %s', (raw) => {
  expect(() => parseAccountCredentials(raw)).toThrow();
  try {
    parseAccountCredentials(raw);
  } catch (error) {
    expect(String(error)).not.toContain('canary');
  }
});

it('accepts explicit Claude token-only mode without requiring invented expiry or rotation', () => {
  expect(parseAccountCredentials('{"mode":"token_only","access_token":"synthetic-oauth"}')).toEqual(
    { mode: 'token_only', access_token: 'synthetic-oauth' },
  );
  expect(() => parseAccountCredentials('{"mode":"apikey","access_token":"synthetic"}')).toThrow();
});
