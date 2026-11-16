import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getByLabelText,
  getByRole,
  queryByLabelText,
  waitFor,
} from '@testing-library/preact';
import { ProxyAPIError, ProxyClient, type ProxyState } from '../src/api/proxy-client';
import {
  ClaudeConnectClient,
  ClaudeConnectError,
  newClaudeConnectCapability,
  type ClaudeConnectRequest,
} from '../src/api/proxy-connect';
import type { PasskeyPlatform } from '../src/hub/platform/passkeys';
import { connectView } from './connect';
import { mountProxy } from './app';
import type { UIContext } from './dom';

const id = 'a'.repeat(32);
const code = 'abcd1234';
const proxyToken = `sk-tlp-${'c'.repeat(64)}`;
const upstreamToken = 'sk-ant-oat01-private-upstream-canary';
function snapshot(): ProxyState {
  return {
    auth: 'passkey',
    mode: 'ui',
    revision: 1,
    routes: [],
    callers: [],
    secrets: [],
    csrf: 'fresh-csrf',
  };
}
const cleanups: (() => void)[] = [];
function setup(
  options: {
    resolution?: ClaudeConnectRequest['resolved_action'];
    sourceAccount?: string;
    approvedSourceAccount?: string;
    link?: { id: string; code?: string };
    inspect?: Partial<ClaudeConnectRequest>;
    approvedResolution?: ClaudeConnectRequest['resolved_action'];
    uncertainCommit?: boolean;
    lostAck?: boolean;
    ackStatus?: 'acknowledged' | 'expired';
    noRetainedToken?: boolean;
    receiptOrigin?: string;
  } = {},
) {
  const order: string[] = [];
  let record: ClaudeConnectRequest = {
    id,
    code,
    origin: window.location.origin,
    expires: Math.floor(Date.now() / 1000) + 300,
    status: 'pending',
    action: 'auto',
    resolved_action: options.resolution ?? 'add',
    ...(options.sourceAccount ? { source_account: options.sourceAccount } : {}),
    account: 'claude',
    client_name: 'Claude computer',
    route: 'claude',
    prefix: '/claude/',
  };
  let result: { token?: string; caller_id?: string; gateway_url: string } | undefined;
  const response = (value: unknown) =>
    new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
  const fetcher = vi.fn<typeof fetch>(async (url, init) => {
    const path = String(url).replace('/_proxy/api/connect/', '').split('?')[0];
    order.push(path!);
    const data = init?.body ? JSON.parse(String(init.body)) : undefined;
    switch (path) {
      case 'start': {
        record = {
          ...record,
          id: data.id,
          account: data.account,
          client_name: data.client_name,
          action: data.action,
        };
        const { resolved_action: _private, source_account: _source, ...publicPending } = record;
        expect(_source).toBe(options.sourceAccount);
        expect(_private).toBeDefined();
        return response(publicPending);
      }
      case 'request':
        return response({ ...record, ...options.inspect });
      case 'approve':
        expect(data).toEqual({ id: record.id, code: record.code });
        expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('fresh-csrf');
        record = {
          ...record,
          status: 'approved',
          resolved_action: options.approvedResolution ?? record.resolved_action,
          ...(options.approvedSourceAccount
            ? { source_account: options.approvedSourceAccount }
            : {}),
        };
        return response(record);
      case 'commit':
        record = { ...record, status: 'complete' };
        result = {
          gateway_url: `${options.receiptOrigin ?? record.origin}${record.prefix}`,
          ...(record.resolved_action === 'reconnect' && record.client_name === ''
            ? {}
            : { token: proxyToken, caller_id: 'c_device' }),
        };
        if (options.noRetainedToken) delete result.token;
        if (options.uncertainCommit) throw new Error(`transport outcome unknown ${upstreamToken}`);
        return response({ ...record, result });
      case 'status':
        return response({ ...record, result });
      case 'ack':
        if (result) delete result.token;
        if (options.ackStatus) record = { ...record, status: options.ackStatus };
        if (options.lostAck) throw new Error('ack reply lost');
        return response({ ...record, result });
      case 'cancel':
        record = { ...record, status: 'canceled' };
        return response(record);
      default:
        throw new Error('unexpected test endpoint');
    }
  });
  const client = new ClaudeConnectClient(fetcher);
  const api = new ProxyClient(vi.fn());
  vi.spyOn(api, 'state').mockImplementation(async () => {
    order.push('state');
    return snapshot();
  });
  vi.spyOn(api, 'authStatus').mockResolvedValue({
    auth: 'passkey',
    setup_required: false,
    bootstrap_available: false,
    recovery_available: false,
    bootstrap_verified: false,
    recovery_verified: false,
    csrf: 'initial',
    origin: window.location.origin,
  });
  vi.spyOn(api, 'beginAssertion').mockImplementation(async () => {
    order.push('begin');
    return { publicKey: { challenge: 'fixture' } } as never;
  });
  vi.spyOn(api, 'finishPasskey').mockImplementation(async () => {
    order.push('finish');
  });
  const platform: PasskeyPlatform = {
    available: () => true,
    create: vi.fn(),
    get: vi.fn(async () => {
      order.push('get');
      return { id: 'assertion' } as never;
    }),
  };
  const protectToken = vi.fn();
  const announce = vi.fn();
  const context: UIContext = {
    run: async (action) => {
      try {
        await action();
        return true;
      } catch {
        announce('fixed action failure');
        return false;
      }
    },
    refresh: vi.fn(),
    navigate: vi.fn(),
    announce,
    protectToken,
  };
  const controller = connectView(snapshot(), api, context, platform, {
    client,
    ...(options.link ? { link: options.link } : {}),
  });
  document.body.append(controller.element);
  cleanups.push(() => controller.dispose());
  return {
    view: controller.element,
    controller,
    fetcher,
    client,
    api,
    platform,
    context,
    protectToken,
    order,
    getRecord: () => record,
  };
}
function calls(fetcher: ReturnType<typeof setup>['fetcher'], path: string) {
  return fetcher.mock.calls.filter(
    ([url]) => String(url).split('?')[0] === `/_proxy/api/connect/${path}`,
  );
}
async function begin(view: HTMLElement) {
  fireEvent.click(getByRole(view, 'button', { name: 'Continue' }));
  await waitFor(() => expect(view.textContent).toContain('Connection code:'));
}
async function completeAdd(view: HTMLElement) {
  await begin(view);
  const token = getByLabelText(view, 'Private Claude subscription token') as HTMLInputElement;
  fireEvent.input(token, { target: { value: upstreamToken } });
  fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
  await waitFor(() => expect(getByLabelText(view, 'Scoped proxy token — shown once')).toBeTruthy());
  return token;
}
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  document.body.replaceChildren();
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('Claude token-only connection', () => {
  it('starts without a token, privately resolves, clears paste before fresh auth and shows only scoped result', async () => {
    const { view, fetcher, platform, api, order, protectToken } = setup();
    const storage = vi.spyOn(Storage.prototype, 'setItem');
    expect(view.querySelector('input[type="password"]')).toBeNull();
    await begin(view);
    expect(calls(fetcher, 'start')).toHaveLength(1);
    expect(calls(fetcher, 'request')).toHaveLength(1);
    expect(calls(fetcher, 'request')[0]?.[1]?.credentials).toBe('same-origin');
    expect(new Headers(calls(fetcher, 'request')[0]?.[1]?.headers).has('Authorization')).toBe(
      false,
    );
    const token = getByLabelText(view, 'Private Claude subscription token') as HTMLInputElement;
    expect(token.type).toBe('password');
    expect(view.textContent).toContain('claude setup-token');
    expect(view.textContent).not.toContain('expires_at');
    vi.mocked(platform.get).mockImplementation(async () => {
      expect(token.value).toBe('');
      order.push('get');
      return { id: 'assertion' } as never;
    });
    fireEvent.input(token, { target: { value: upstreamToken } });
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() =>
      expect(getByLabelText(view, 'Scoped proxy token — shown once')).toBeTruthy(),
    );
    expect(api.beginAssertion).toHaveBeenCalledWith('reauth');
    expect(order.indexOf('finish')).toBeLessThan(order.indexOf('approve'));
    expect(order.indexOf('finish')).toBeLessThan(order.indexOf('state'));
    expect(order.indexOf('approve')).toBeLessThan(order.indexOf('commit'));
    expect(JSON.parse(String(calls(fetcher, 'commit')[0]?.[1]?.body))).toEqual({
      id: setupId(fetcher),
      token: upstreamToken,
    });
    expect(
      (getByLabelText(view, 'Scoped proxy token — shown once') as HTMLInputElement).value,
    ).toBe(proxyToken);
    expect(
      [...view.querySelectorAll('input')].some((input) => input.value.includes(upstreamToken)),
    ).toBe(false);
    expect(protectToken).toHaveBeenLastCalledWith(true, false);
    expect(storage).not.toHaveBeenCalled();
    expect(window.location.href).not.toContain(upstreamToken);
  });
  it('pairs an existing working Claude account without setup-token, paste or replacement', async () => {
    const { view, fetcher } = setup({ resolution: 'device' });
    await begin(view);
    expect(queryByLabelText(view, 'Private Claude subscription token')).toBeNull();
    expect(view.textContent).not.toContain('claude setup-token');
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect this device' }));
    await waitFor(() =>
      expect(getByLabelText(view, 'Scoped proxy token — shown once')).toBeTruthy(),
    );
    expect(JSON.parse(String(calls(fetcher, 'commit')[0]?.[1]?.body))).toEqual({
      id: setupId(fetcher),
    });
  });

  it('shows a legacy Claude source only after authenticated resolution and pairs without a token', async () => {
    const { view, fetcher } = setup({ resolution: 'device', sourceAccount: 'legacy-claude' });
    expect(view.textContent).not.toContain('legacy-claude');
    await begin(view);
    expect(view.textContent).toContain('Using existing Claude account legacy-claude');
    expect(queryByLabelText(view, 'Private Claude subscription token')).toBeNull();
    expect(view.textContent).not.toContain('claude setup-token');
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect this device' }));
    await waitFor(() =>
      expect(getByLabelText(view, 'Scoped proxy token — shown once')).toBeTruthy(),
    );
    expect(JSON.parse(String(calls(fetcher, 'approve')[0]?.[1]?.body))).toEqual({
      id: setupId(fetcher),
      code,
    });
    expect(JSON.parse(String(calls(fetcher, 'commit')[0]?.[1]?.body))).toEqual({
      id: setupId(fetcher),
    });
  });
  it('does not silently commit if the frozen legacy source changes after preview', async () => {
    const { view, fetcher } = setup({
      resolution: 'device',
      sourceAccount: 'legacy-one',
      approvedSourceAccount: 'legacy-two',
    });
    await begin(view);
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect this device' }));
    await waitFor(() => expect(view.textContent).toContain('The connection changed'));
    expect(calls(fetcher, 'commit')).toHaveLength(0);
    expect(calls(fetcher, 'start')).toHaveLength(1);
  });
  it('uses account-only reconnect by default and never creates or displays a ghost device token', async () => {
    const { view, fetcher, protectToken } = setup({ resolution: 'reconnect' });
    fireEvent.change(getByLabelText(view, 'Connection'), { target: { value: 'reconnect' } });
    expect((getByLabelText(view, 'Computer name') as HTMLInputElement).value).toBe('');
    await begin(view);
    expect(view.textContent).toContain(
      'no automatic refresh, even when replacing an existing rotating credential',
    );
    const start = JSON.parse(String(calls(fetcher, 'start')[0]?.[1]?.body));
    expect(start.action).toBe('reconnect');
    expect(start.client_name).toBe('');
    const token = getByLabelText(view, 'Private Claude subscription token') as HTMLInputElement;
    fireEvent.input(token, { target: { value: upstreamToken } });
    fireEvent.submit(token.closest('form')!);
    expect(token.value).toBe('');
    expect(calls(fetcher, 'approve')).toHaveLength(0);
    fireEvent.click(
      getByLabelText(
        view,
        'Reconnect this account explicitly; existing devices keep their proxy credentials.',
      ),
    );
    fireEvent.input(token, { target: { value: upstreamToken } });
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() =>
      expect(view.textContent).toContain('Existing devices keep their proxy credentials.'),
    );
    await waitFor(() =>
      expect(getByRole(view, 'button', { name: 'Finish reconnect' })).toBeTruthy(),
    );
    await waitFor(() =>
      expect(queryByLabelText(view, 'Scoped proxy token — shown once')).toBeNull(),
    );
    expect(protectToken).toHaveBeenLastCalledWith(false, false);
  });
  it('recovers a persisted receipt after an uncertain commit without restarting or reminting', async () => {
    const { view, fetcher } = setup({ uncertainCommit: true });
    await completeAdd(view);
    expect(calls(fetcher, 'start')).toHaveLength(1);
    expect(calls(fetcher, 'commit')).toHaveLength(1);
    expect(calls(fetcher, 'status')).toHaveLength(1);
    expect(view.textContent).not.toContain(upstreamToken);
  });
  it('does not send or discard a pasted token silently when approval resolves to device instead of add', async () => {
    const { view, fetcher } = setup({ resolution: 'add', approvedResolution: 'device' });
    await begin(view);
    fireEvent.input(getByLabelText(view, 'Private Claude subscription token'), {
      target: { value: upstreamToken },
    });
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() =>
      expect(view.textContent).toContain('Your pasted subscription token was not sent'),
    );
    expect(calls(fetcher, 'commit')).toHaveLength(0);
    expect(calls(fetcher, 'start')).toHaveLength(1);
    fireEvent.click(
      await waitFor(() => getByRole(view, 'button', { name: 'Review this same connection' })),
    );
    await waitFor(() => expect(view.textContent).toContain('Claude is already connected'));
    expect(view.querySelector('input[type="password"]')).toBeNull();
    expect(calls(fetcher, 'commit')).toHaveLength(0);
  });
  it('keeps the one-time result guarded until acknowledgment and recovers a lost ack reply', async () => {
    const { view, fetcher, protectToken } = setup({ lostAck: true });
    await completeAdd(view);
    expect(protectToken).toHaveBeenLastCalledWith(true, false);
    fireEvent.click(getByRole(view, 'button', { name: 'I saved it — finish' }));
    await waitFor(() => expect(view.textContent).toContain('already saved or expired'));
    await waitFor(() =>
      expect(queryByLabelText(view, 'Scoped proxy token — shown once')).toBeNull(),
    );
    expect(protectToken).toHaveBeenLastCalledWith(false, false);
    expect(calls(fetcher, 'commit')).toHaveLength(1);
    expect(calls(fetcher, 'ack')).toHaveLength(1);
  });

  it.each(['acknowledged', 'expired'] as const)(
    'recovers %s metadata after uncertain acknowledgment without exposing a token or reminting',
    async (status) => {
      const { view, fetcher, protectToken } = setup({ lostAck: true, ackStatus: status });
      await completeAdd(view);
      fireEvent.click(getByRole(view, 'button', { name: 'I saved it — finish' }));
      await waitFor(() =>
        expect(queryByLabelText(view, 'Scoped proxy token — shown once')).toBeNull(),
      );
      expect(view.textContent).toContain(
        status === 'acknowledged' ? 'retained device token has been cleared' : 'request is expired',
      );
      expect(protectToken).toHaveBeenLastCalledWith(false, false);
      expect(calls(fetcher, 'start')).toHaveLength(1);
      expect(calls(fetcher, 'commit')).toHaveLength(1);
      expect(calls(fetcher, 'status')).toHaveLength(1);
    },
  );
  it('does not remint an already-completed token that is no longer retained', async () => {
    const { view, fetcher } = setup({ noRetainedToken: true });
    await begin(view);
    fireEvent.input(getByLabelText(view, 'Private Claude subscription token'), {
      target: { value: upstreamToken },
    });
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() => expect(view.textContent).toContain('It will not be minted again'));
    expect(calls(fetcher, 'commit')).toHaveLength(1);
    expect(calls(fetcher, 'start')).toHaveLength(1);
  });
  it('sanitizes browser failures and clears the password even when fresh auth is cancelled', async () => {
    const { view, platform, fetcher } = setup();
    await begin(view);
    vi.mocked(platform.get).mockRejectedValue(new DOMException(upstreamToken, 'NotAllowedError'));
    const token = getByLabelText(view, 'Private Claude subscription token') as HTMLInputElement;
    fireEvent.input(token, { target: { value: upstreamToken } });
    fireEvent.click(getByRole(view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() => expect(view.textContent).toContain('Could not confirm this connection'));
    expect(token.value).toBe('');
    expect(view.textContent).not.toContain(upstreamToken);
    expect(calls(fetcher, 'approve')).toHaveLength(0);
    expect(calls(fetcher, 'commit')).toHaveLength(0);
  });
  it('never adopts unmanaged collisions and never displays a cross-origin gateway credential', async () => {
    const collision = setup({ resolution: 'collision' });
    fireEvent.click(getByRole(collision.view, 'button', { name: 'Continue' }));
    await waitFor(() => expect(collision.view.textContent).toContain('Nothing was replaced'));
    expect(calls(collision.fetcher, 'approve')).toHaveLength(0);
    const wrong = setup({ receiptOrigin: 'https://foreign.example' });
    await begin(wrong.view);
    fireEvent.input(getByLabelText(wrong.view, 'Private Claude subscription token'), {
      target: { value: upstreamToken },
    });
    fireEvent.click(getByRole(wrong.view, 'button', { name: 'Approve and connect Claude' }));
    await waitFor(() => expect(calls(wrong.fetcher, 'status')).toHaveLength(1));
    expect(queryByLabelText(wrong.view, 'Scoped proxy token — shown once')).toBeNull();
    expect(wrong.view.textContent).not.toContain(proxyToken);
  });
});
function setupId(fetcher: ReturnType<typeof setup>['fetcher']): string {
  return JSON.parse(String(calls(fetcher, 'start')[0]?.[1]?.body)).id;
}

describe('CLI consent link', () => {
  it.each([undefined, 'deadbeef', code])(
    'requires manual terminal proof and ignores URL code %s',
    async (urlCode) => {
      const { view, fetcher, api, order } = setup({
        link: { id, code: urlCode },
        resolution: 'add',
      });
      const approve = await waitFor(() => getByRole(view, 'button', { name: 'Approve add' }));
      const manual = getByLabelText(view, 'Code from your terminal') as HTMLInputElement;
      expect(manual.value).toBe('');
      expect(view.querySelector('input[type="password"]')).toBeNull();
      expect(view.textContent).not.toContain(code);
      expect(view.textContent).not.toContain('deadbeef');
      expect(view.textContent).toContain(
        'type the code from your own terminal, not a link/message',
      );
      for (const invalid of ['', 'abcd', 'zzzzzzzz', 'abcd1235']) {
        fireEvent.input(manual, { target: { value: invalid } });
        // Submit directly to exercise the handler even if native pattern validation is bypassed.
        fireEvent.submit(manual.closest('form')!);
        expect(api.beginAssertion).not.toHaveBeenCalled();
        expect(calls(fetcher, 'approve')).toHaveLength(0);
        expect(view.textContent).not.toContain(code);
      }
      fireEvent.input(manual, { target: { value: code } });
      fireEvent.click(approve);
      await waitFor(() =>
        expect(view.textContent).toContain('Return to the requesting computer to finish'),
      );
      expect(api.beginAssertion).toHaveBeenCalledExactlyOnceWith('reauth');
      expect(calls(fetcher, 'approve')).toHaveLength(1);
      expect(JSON.parse(String(calls(fetcher, 'approve')[0]?.[1]?.body))).toEqual({ id, code });
      expect(order.indexOf('finish')).toBeLessThan(order.indexOf('approve'));
      expect(view.textContent).not.toContain(code);
      expect(calls(fetcher, 'start')).toHaveLength(0);
      expect(calls(fetcher, 'commit')).toHaveLength(0);
    },
  );
  it.each([{ id: 'b'.repeat(32) }, { origin: 'https://wrong.example' }])(
    'rejects mismatched consent binding %j',
    async (override) => {
      const { view, fetcher, api } = setup({ link: { id }, inspect: override });
      await waitFor(() => expect(view.textContent).toContain('could not be confirmed'));
      expect(calls(fetcher, 'approve')).toHaveLength(0);
      expect(api.beginAssertion).not.toHaveBeenCalled();
      expect(view.querySelector('input[type="password"]')).toBeNull();
    },
  );
  it('displays expired and already-approved requests without replaying approval', async () => {
    const expired = setup({
      link: { id, code },
      inspect: { expires: Math.floor(Date.now() / 1000) - 1 },
    });
    await waitFor(() => expect(expired.view.textContent).toContain('request expired'));
    expect(calls(expired.fetcher, 'approve')).toHaveLength(0);
    const replay = setup({ link: { id, code }, inspect: { status: 'approved' } });
    await waitFor(() => expect(replay.view.textContent).toContain('already approved'));
    expect(calls(replay.fetcher, 'approve')).toHaveLength(0);
  });
});

describe('capability transport and header integration', () => {
  it('uses separate bearer capabilities, no cookies/storage/secret URLs and fixed errors', async () => {
    const cap = newClaudeConnectCapability();
    expect(cap.id).toMatch(/^[a-f0-9]{32}$/);
    expect(cap.secret).toMatch(/^[a-f0-9]{64}$/);
    const fetcher = vi.fn<typeof fetch>().mockImplementation(
      async () =>
        new Response(
          JSON.stringify({
            id: cap.id,
            code,
            origin: window.location.origin,
            expires: Math.floor(Date.now() / 1000) + 300,
            status: 'pending',
            action: 'auto',
            account: 'claude',
            client_name: 'Claude computer',
            route: 'claude',
            prefix: '/claude/',
          }),
        ),
    );
    const client = new ClaudeConnectClient(fetcher);
    await client.start(cap, { account: 'claude', client_name: 'Claude computer', action: 'auto' });
    await client.status(cap);
    expect(fetcher.mock.calls[0]?.[1]?.credentials).toBe('omit');
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body)).secret).toBe(cap.secret);
    expect(fetcher.mock.calls[1]?.[1]).toEqual(
      expect.objectContaining({
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
        headers: expect.objectContaining({ Authorization: `Bearer ${cap.secret}` }),
      }),
    );
    expect(String(fetcher.mock.calls[1]?.[0])).not.toContain(cap.secret);
    fetcher.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: upstreamToken }), { status: 409 }),
    );
    await expect(client.commit(cap, upstreamToken)).rejects.toThrow(ClaudeConnectError);
    fetcher.mockRejectedValueOnce(new Error(upstreamToken));
    await expect(client.status(cap)).rejects.not.toThrow(upstreamToken);
  });

  it('accepts nonsecret acknowledged/expired metadata but never an expired retained token', async () => {
    const cap = { id, secret: 'd'.repeat(64) };
    const metadata = {
      id,
      code,
      origin: window.location.origin,
      expires: Math.floor(Date.now() / 1000) + 300,
      action: 'auto',
      resolved_action: 'device',
      account: 'claude',
      client_name: 'Claude computer',
      route: 'claude',
      prefix: '/claude/',
      result: { gateway_url: `${window.location.origin}/claude/`, caller_id: 'c_device' },
    };
    const fetcher = vi.fn<typeof fetch>();
    const client = new ClaudeConnectClient(fetcher);
    for (const status of ['acknowledged', 'expired']) {
      fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ ...metadata, status })));
      const receipt = await client.status(cap);
      expect(receipt.status).toBe(status);
      expect(receipt.result?.token).toBeUndefined();
      expect(receipt.result?.caller_id).toBe('c_device');
    }
    fetcher.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          ...metadata,
          status: 'expired',
          result: { ...metadata.result, token: proxyToken },
        }),
      ),
    );
    await expect(client.status(cap)).rejects.toThrow(ClaudeConnectError);
    expect(fetcher.mock.calls.every((call) => String(call[0]).includes('/status?'))).toBe(true);
  });
  it.each(['', '&code=deadbeef'])(
    'opens an ID-only link after login, ignoring compatibility code %s',
    async (suffix) => {
      const fixture = setup();
      fixture.controller.dispose();
      fixture.view.remove();
      window.history.replaceState(null, '', `/_proxy/?connect=${id}${suffix}`);
      vi.stubGlobal('fetch', fixture.fetcher);
      cleanups.push(() => vi.unstubAllGlobals());
      const root = document.createElement('div');
      document.body.append(root);
      vi.mocked(fixture.api.state).mockRejectedValueOnce(new ProxyAPIError(401, 'Sign in.'));
      cleanups.push(mountProxy(root, fixture.api, fixture.platform));
      const login = await waitFor(() =>
        getByRole(root, 'button', { name: 'Sign in with passkey' }),
      );
      expect(calls(fixture.fetcher, 'request')).toHaveLength(0);
      fireEvent.click(login);
      await waitFor(() =>
        expect(getByRole(root, 'heading', { name: 'Connect Claude' })).toBeTruthy(),
      );
      expect(getByRole(root, 'button', { name: 'Connect Claude' })).toBeTruthy();
      await waitFor(() => expect(getByRole(root, 'button', { name: 'Approve add' })).toBeTruthy());
      expect((getByLabelText(root, 'Code from your terminal') as HTMLInputElement).value).toBe('');
      expect(root.textContent).not.toContain(code);
      expect(root.textContent).not.toContain('deadbeef');
      fireEvent.click(getByRole(root, 'button', { name: 'Routes' }));
      await waitFor(() => expect(getByRole(root, 'heading', { name: 'Routes' })).toBeTruthy());
      expect(window.location.search).toBe('?tab=routes');
    },
  );
  it('preserves the one-time capability across header refresh and existing navigation guards', async () => {
    const fixture = setup();
    fixture.controller.dispose();
    fixture.view.remove();
    window.history.replaceState(null, '', '/_proxy/?tab=routes');
    vi.stubGlobal('fetch', fixture.fetcher);
    cleanups.push(() => vi.unstubAllGlobals());
    const root = document.createElement('div');
    document.body.append(root);
    cleanups.push(mountProxy(root, fixture.api, fixture.platform));
    fireEvent.click(await waitFor(() => getByRole(root, 'button', { name: 'Connect Claude' })));
    await completeAdd(root);
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    fireEvent.click(getByRole(root, 'button', { name: 'Routes' }));
    expect(confirm).toHaveBeenCalledOnce();
    expect(getByRole(root, 'heading', { name: 'Connect Claude' })).toBeTruthy();
    const before = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(before);
    expect(before.defaultPrevented).toBe(true);
    confirm.mockReturnValue(true);
    fireEvent.click(getByRole(root, 'button', { name: 'Refresh state' }));
    await waitFor(() =>
      expect(
        (getByLabelText(root, 'Scoped proxy token — shown once') as HTMLInputElement).value,
      ).toBe(proxyToken),
    );
    expect(calls(fixture.fetcher, 'start')).toHaveLength(1);
    expect(calls(fixture.fetcher, 'commit')).toHaveLength(1);
    const stillProtected = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(stillProtected);
    expect(stillProtected.defaultPrevented).toBe(true);
    fireEvent.click(getByRole(root, 'button', { name: 'I saved it — finish' }));
    await waitFor(() =>
      expect(queryByLabelText(root, 'Scoped proxy token — shown once')).toBeNull(),
    );
    const after = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(after);
    expect(after.defaultPrevented).toBe(false);
    fireEvent.click(getByRole(root, 'button', { name: 'Routes' }));
    await waitFor(() => expect(getByRole(root, 'heading', { name: 'Routes' })).toBeTruthy());
  });
});
