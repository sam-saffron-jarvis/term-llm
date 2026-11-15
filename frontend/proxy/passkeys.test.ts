import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import {
  ProxyAPIError,
  ProxyClient,
  type ProxyAuthStatus,
  type ProxyState,
} from '../src/api/proxy-client';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import type {
  SerializedPublicKeyCredential,
  WebAuthnCreationWireOptions,
  WebAuthnRequestWireOptions,
} from '../src/hub/domain/types';
import { mountProxy } from './app';

const status: ProxyAuthStatus = {
  auth: 'passkey',
  setup_required: false,
  bootstrap_available: false,
  recovery_available: true,
  bootstrap_verified: false,
  recovery_verified: false,
  csrf: 'pre-auth',
  origin: 'https://proxy.example',
};
const snapshot: ProxyState = {
  auth: 'passkey',
  mode: 'ui',
  revision: 1,
  csrf: 'session',
  routes: [],
  callers: [],
  secrets: [],
};
const credential = { id: 'private-credential' } as SerializedPublicKeyCredential;
const creation = { publicKey: { challenge: 'create' } } as WebAuthnCreationWireOptions;
const assertion = { publicKey: { challenge: 'get' } } as WebAuthnRequestWireOptions;
const disposals: (() => void)[] = [];
function setup(options: Partial<ProxyAuthStatus> = {}, authenticated = false) {
  const api = new ProxyClient(vi.fn());
  vi.spyOn(api, 'authStatus').mockImplementation(async () => ({ ...status, ...options }));
  const state = vi.spyOn(api, 'state').mockResolvedValue(snapshot);
  if (!authenticated) state.mockRejectedValueOnce(new ProxyAPIError(401, 'Sign in.'));
  vi.spyOn(api, 'verifySecret').mockResolvedValue();
  vi.spyOn(api, 'beginEnrollment').mockResolvedValue(creation);
  vi.spyOn(api, 'beginAssertion').mockResolvedValue(assertion);
  vi.spyOn(api, 'finishPasskey').mockResolvedValue();
  vi.spyOn(api, 'credentials').mockResolvedValue({
    credentials: [
      { id: 'one', name: 'Laptop', created_at: '2026-11-01' },
      { id: 'two', name: 'Phone', created_at: '2026-11-02' },
    ],
    sessions: 3,
  });
  vi.spyOn(api, 'deleteCredential').mockResolvedValue();
  vi.spyOn(api, 'revokeSessions').mockResolvedValue();
  const platform: PasskeyPlatform = {
    available: vi.fn(() => true),
    create: vi.fn().mockResolvedValue(credential),
    get: vi.fn().mockResolvedValue(credential),
  };
  const root = document.createElement('div');
  document.body.append(root);
  disposals.push(mountProxy(root, api, platform));
  return { api, root, platform };
}
async function click(root: HTMLElement, name: string) {
  fireEvent.click(await waitFor(() => getByRole(root, 'button', { name })));
}
function fill(root: HTMLElement, label: string, value: string) {
  fireEvent.input(getByLabelText(root, label), { target: { value } });
}
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  document.body.replaceChildren();
  vi.restoreAllMocks();
});
describe('proxy passkey access', () => {
  it('fetches status first and uses passkey login without offering a token fallback', async () => {
    const { api, root, platform } = setup();
    const tokenLogin = vi.spyOn(api, 'login');
    await click(root, 'Sign in with passkey');
    await waitFor(() => expect(root.textContent).toContain('Revision 1'));
    expect(vi.mocked(api.authStatus).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(api.state).mock.invocationCallOrder[0],
    );
    expect(api.beginAssertion).toHaveBeenCalledWith('login');
    expect(platform.get).toHaveBeenCalledWith(assertion);
    expect(api.finishPasskey).toHaveBeenCalledWith('login', credential);
    expect(tokenLogin).not.toHaveBeenCalled();
    expect(root.textContent).not.toContain('Administrator token');
    expect(getByRole(root, 'button', { name: 'Passkeys' })).toBeTruthy();
  });
  it('clears the bootstrap secret before verification and establishes a session after enrollment', async () => {
    const { api, root, platform } = setup({ setup_required: true, bootstrap_available: true });
    const token = (await waitFor(() =>
      getByLabelText(root, 'Bootstrap token'),
    )) as HTMLInputElement;
    vi.mocked(api.verifySecret).mockImplementation(async () => {
      expect(token.value).toBe('');
    });
    fill(root, 'Bootstrap token', 'private-file-secret');
    fill(root, 'Passkey name', 'Laptop');
    await click(root, 'Create first passkey');
    await waitFor(() => expect(root.textContent).toContain('Revision 1'));
    expect(api.verifySecret).toHaveBeenCalledWith('bootstrap', 'private-file-secret');
    expect(api.beginEnrollment).toHaveBeenCalledWith('bootstrap', 'Laptop');
    expect(platform.create).toHaveBeenCalledWith(creation);
    expect(api.finishPasskey).toHaveBeenCalledWith('bootstrap', credential);
    expect(root.textContent).not.toContain('private-file-secret');
  });
  it('recovery enrolls only, clears the secret and requires a new sign-in', async () => {
    const { api, root } = setup();
    await click(root, 'Recover access');
    const token = getByLabelText(root, 'Recovery token') as HTMLInputElement;
    vi.mocked(api.verifySecret).mockImplementation(async () => {
      expect(token.value).toBe('');
    });
    fill(root, 'Recovery token', 'private-recovery');
    fill(root, 'Passkey name', 'Replacement');
    await click(root, 'Recover with new passkey');
    await waitFor(() => expect(root.textContent).toContain('All sessions were invalidated'));
    expect(api.finishPasskey).toHaveBeenCalledWith('recovery', credential);
    expect(api.state).toHaveBeenCalledTimes(1);
    expect(api.beginAssertion).not.toHaveBeenCalled();
    expect(getByRole(root, 'button', { name: 'Sign in with passkey' })).toBeTruthy();
    expect(root.textContent).not.toContain('private-recovery');
  });
  it.each(['bootstrap', 'recovery'] as const)(
    'retries %s creation after cancellation without exchanging the one-use secret again',
    async (kind) => {
      const bootstrap = kind === 'bootstrap';
      const { api, root, platform } = setup({
        setup_required: bootstrap,
        bootstrap_available: bootstrap,
      });
      vi.mocked(platform.create).mockRejectedValueOnce(
        new DOMException('cancelled', 'NotAllowedError'),
      );
      if (!bootstrap) await click(root, 'Recover access');
      const label = bootstrap ? 'Bootstrap token' : 'Recovery token';
      await waitFor(() => getByLabelText(root, label));
      fill(root, label, 'one-use-secret');
      fill(root, 'Passkey name', 'Retry key');
      const action = bootstrap ? 'Create first passkey' : 'Recover with new passkey';
      await click(root, action);
      await waitFor(() => expect(root.textContent).toContain('cancelled or timed out'));
      expect(root.querySelector('input[type=password]')).toBeNull();
      expect(root.textContent).toContain('Secret verified.');
      expect(api.verifySecret).toHaveBeenCalledOnce();
      expect(api.finishPasskey).not.toHaveBeenCalled();
      if (!bootstrap) {
        await click(root, 'Back to sign in');
        expect(getByRole(root, 'button', { name: 'Sign in with passkey' })).toBeTruthy();
        await click(root, 'Recover access');
        expect(root.querySelector('input[type=password]')).toBeNull();
        fill(root, 'Passkey name', 'Retry key');
      }
      await click(root, action);
      await waitFor(() => expect(api.finishPasskey).toHaveBeenCalledWith(kind, credential));
      expect(api.verifySecret).toHaveBeenCalledOnce();
      expect(api.beginEnrollment).toHaveBeenCalledTimes(2);
      expect(platform.create).toHaveBeenCalledTimes(2);
    },
  );
  it.each(['bootstrap', 'recovery'] as const)(
    'resumes %s after reload with an active grant and consumed root secret',
    async (kind) => {
      const bootstrap = kind === 'bootstrap';
      const { api, root } = setup({
        setup_required: bootstrap,
        bootstrap_available: bootstrap,
        bootstrap_verified: bootstrap,
        recovery_verified: !bootstrap,
      });
      await waitFor(() => expect(root.textContent).toContain('Secret verified.'));
      expect(root.querySelector('input[type=password]')).toBeNull();
      fill(root, 'Passkey name', 'Resumed key');
      await click(root, bootstrap ? 'Create first passkey' : 'Recover with new passkey');
      await waitFor(() => expect(api.finishPasskey).toHaveBeenCalledWith(kind, credential));
      expect(api.verifySecret).not.toHaveBeenCalled();
      expect(api.beginEnrollment).toHaveBeenCalledWith(kind, 'Resumed key');
    },
  );
  it('does not echo browser exception messages or fall back to tokens', async () => {
    const { root, platform } = setup();
    vi.mocked(platform.get).mockRejectedValue(new Error('serialized private-credential'));
    await click(root, 'Sign in with passkey');
    await waitFor(() => expect(root.textContent).toContain('browser could not complete'));
    expect(root.textContent).not.toContain('private-credential');
    expect(root.textContent).not.toContain('Administrator token');
  });
  it('shows an unavailable-browser message without starting an assertion', async () => {
    const { root, api, platform } = setup();
    vi.mocked(platform.available).mockReturnValue(false);
    await waitFor(() => expect(root.textContent).toContain('Passkeys are not available'));
    expect(api.beginAssertion).not.toHaveBeenCalled();
    expect(queryByRole(root, 'button', { name: 'Sign in with passkey' })).toBeNull();
  });
  it('keeps token mode explicit with no Passkeys tab', async () => {
    const { root, api } = setup({ auth: 'token' }, true);
    vi.mocked(api.state).mockResolvedValue({ ...snapshot, auth: 'token' });
    await click(root, 'Routes');
    expect(queryByRole(root, 'button', { name: 'Passkeys' })).toBeNull();
  });
});
describe('proxy passkey security', () => {
  it('reauthenticates before enrollment, deletion and revoking other sessions', async () => {
    const { root, api } = setup({}, true);
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    await click(root, 'Passkeys');
    await waitFor(() => expect(root.textContent).toContain('3 active sessions'));
    fill(root, 'Passkey name', 'New key');
    await click(root, 'Add passkey');
    await waitFor(() => expect(root.textContent).toContain('Passkey added.'));
    await click(root, 'Delete Laptop');
    await waitFor(() => expect(root.textContent).toContain('Passkey deleted.'));
    await click(root, 'Revoke other sessions');
    await waitFor(() => expect(root.textContent).toContain('Other sessions revoked.'));
    expect(api.beginAssertion).toHaveBeenCalledTimes(3);
    expect(api.beginAssertion).toHaveBeenNthCalledWith(1, 'reauth');
    const finishes = vi.mocked(api.finishPasskey).mock.invocationCallOrder;
    expect(finishes[0]).toBeLessThan(vi.mocked(api.beginEnrollment).mock.invocationCallOrder[0]);
    expect(finishes[2]).toBeLessThan(vi.mocked(api.deleteCredential).mock.invocationCallOrder[0]);
    expect(finishes[3]).toBeLessThan(vi.mocked(api.revokeSessions).mock.invocationCallOrder[0]);
  });
  it('does not mutate after a cancelled reauth and disables deleting the last passkey', async () => {
    const { api, root, platform } = setup({}, true);
    vi.mocked(api.credentials).mockResolvedValue({
      credentials: [{ id: 'one', name: 'Only', created_at: 'today' }],
      sessions: 1,
    });
    vi.mocked(platform.get).mockRejectedValue(new DOMException('sensitive', 'NotAllowedError'));
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    await click(root, 'Passkeys');
    await waitFor(() => expect(getByRole(root, 'button', { name: 'Delete Only' })).toBeDisabled());
    await click(root, 'Revoke other sessions');
    await waitFor(() => expect(root.textContent).toContain('cancelled or timed out'));
    expect(api.revokeSessions).not.toHaveBeenCalled();
    expect(api.deleteCredential).not.toHaveBeenCalled();
    expect(root.textContent).not.toContain('sensitive');
  });
});

describe('proxy passkey transport', () => {
  it('uses status CSRF for private-secret verification and all ceremony mutations, then rotates it', async () => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (url) => {
      if (String(url).endsWith('auth/status')) return Response.json(status);
      if (String(url).endsWith('/finish')) return Response.json({ csrf: 'rotated' });
      return Response.json({});
    });
    const api = new ProxyClient(fetcher);
    await api.authStatus();
    await api.verifySecret('bootstrap', 'secret');
    await api.beginEnrollment('bootstrap', 'Laptop');
    await api.finishPasskey('bootstrap', credential);
    await api.beginAssertion('reauth');
    await api.finishPasskey('reauth', credential);
    await api.deleteCredential('id');
    await api.revokeSessions();
    expect(
      fetcher.mock.calls
        .slice(1, 4)
        .every(([, init]) => new Headers(init?.headers).get('X-CSRF-Token') === 'pre-auth'),
    ).toBe(true);
    expect(
      fetcher.mock.calls
        .slice(4)
        .every(([, init]) => new Headers(init?.headers).get('X-CSRF-Token') === 'rotated'),
    ).toBe(true);
    expect(fetcher.mock.calls[1][1]?.body).toBe('{"token":"secret"}');
    expect(fetcher.mock.calls[3][1]?.body).toBe(JSON.stringify(credential));
    expect(fetcher.mock.calls[6][1]?.method).toBe('DELETE');
    for (const [url, init] of fetcher.mock.calls) {
      expect(url).not.toContain('secret');
      expect(init).toMatchObject({
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
      });
    }
  });
});
