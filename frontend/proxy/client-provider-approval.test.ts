import { afterEach, describe, expect, it, vi } from 'vitest';
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
  type ProxyCaller,
  type ProxyAdminRoute,
  type ProxyState,
} from '../src/api/proxy-client';
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

const caller = (overrides: Partial<ProxyCaller> = {}): ProxyCaller => ({
  id: 'c_one',
  name: 'Workstation',
  created: 1,
  revoked: false,
  grants: [],
  created_by: '',
  external_id: '',
  ...overrides,
});

const sharedAccount: ProxyAccount = {
  name: 'shared-claude',
  kind: 'claude-bin',
  auth_mode: 'shared_vault',
  state: 'needs_login',
  generation: 1,
  updated: 1,
};

function snapshot(overrides: Partial<ProxyState> = {}): ProxyState {
  return {
    defaults: null,
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: [route('chatgpt'), route('chatgpt-backend'), route('venice')],
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'chatgpt',
        label: 'ChatGPT',
        routes: ['chatgpt', 'chatgpt-backend'],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'venice',
        label: 'Venice',
        routes: ['venice'],
      },
    ],
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

function setup(state = snapshot()) {
  const api = new ProxyClient(vi.fn());
  const createProviderCaller = vi.spyOn(api, 'createProviderCaller');
  const updateProviderApprovals = vi.spyOn(api, 'updateProviderApprovals').mockResolvedValue();
  const createCaller = vi.spyOn(api, 'createCaller');
  const updateCaller = vi.spyOn(api, 'updateCaller');
  const announce = vi.fn();
  const toast = vi.fn();
  const protectToken = vi.fn();
  const refresh = vi.fn(async () => state);
  const context: UIContext = {
    run: async (action) => {
      await action();
      return true;
    },
    refresh,
    toast,
    announce,
    navigate: vi.fn(),
    protectToken,
  };
  const root = callersView(state, api, context);
  document.body.append(root);
  return {
    root,
    api,
    createProviderCaller,
    updateProviderApprovals,
    createCaller,
    updateCaller,
    announce,
    toast,
    protectToken,
    refresh,
  };
}

function openEditor(root: HTMLElement): HTMLFormElement {
  fireEvent.click(getByRole(root, 'button', { name: 'Workstation' }));
  return root.querySelector<HTMLFormElement>('.client-detail form')!;
}

describe('provider-wide client approval', () => {
  it('offers providers, not routes, and shows the approved route URLs once', async () => {
    const { root, createProviderCaller, createCaller, announce, protectToken } = setup();
    createProviderCaller.mockResolvedValue({
      token: 'sk-tlp-provider',
      caller: caller({ id: 'c_new', name: 'Alpha', grants: ['chatgpt', 'chatgpt-backend'] }),
    });
    const composer = root.querySelector<HTMLFormElement>('#client-composer')!;
    expect(composer.querySelectorAll('.grant-chip')).toHaveLength(2);
    expect(getByLabelText(composer, 'chatgpt')).not.toBeChecked();
    expect(getByLabelText(composer, 'venice')).not.toBeChecked();
    expect(queryByLabelText(composer, 'chatgpt-backend')).toBeNull();
    // Opening the composer never grants anything by itself.
    expect(createProviderCaller).not.toHaveBeenCalled();

    fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Alpha' } });
    fireEvent.click(getByLabelText(composer, 'chatgpt'));
    fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));

    await waitFor(() => expect(createProviderCaller).toHaveBeenCalledWith('Alpha', ['chatgpt'], 7));
    const token = await waitFor(() => getByLabelText(root, 'Client token') as HTMLInputElement);
    expect(token.value).toBe('sk-tlp-provider');
    expect(token.readOnly).toBe(true);
    expect(token).toHaveFocus();
    expect(protectToken.mock.calls).toEqual([[true, true], [true]]);
    expect(createCaller).not.toHaveBeenCalled();
    // The approved provider's routes are setup details, not extra grants.
    expect(root).toHaveTextContent(`Base URL: ${location.origin}/chatgpt/`);
    expect(root).toHaveTextContent(`Base URL: ${location.origin}/chatgpt-backend/`);
    expect(root).not.toHaveTextContent(`${location.origin}/venice/`);
    expect(getByRole(root, 'heading', { name: 'term-llm client configuration' })).toBeVisible();
    expect(root.querySelector('pre code')?.textContent).toBe(
      `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
    );
    expect(root.textContent).not.toContain('sk-tlp-provider');
    expect(root.querySelector('pre:not(:has(code))')).toHaveTextContent('$TOKEN');
    expect(announce).toHaveBeenLastCalledWith(expect.stringContaining('Copy the token now'));
  });

  it('shows a copyable multi-provider term-llm config.yaml snippet after creating a client', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const { root, createProviderCaller, toast } = setup();
    createProviderCaller.mockResolvedValue({
      token: 'sk-tlp-multi-secret',
      caller: caller({
        id: 'c_multi',
        name: 'Laptop',
        grants: ['chatgpt', 'chatgpt-backend', 'venice'],
      }),
    });
    const composer = root.querySelector<HTMLFormElement>('#client-composer')!;
    fireEvent.input(getByLabelText(composer, 'Client name'), { target: { value: 'Laptop' } });
    fireEvent.click(getByLabelText(composer, 'chatgpt'));
    fireEvent.click(getByLabelText(composer, 'venice'));
    fireEvent.click(getByRole(composer, 'button', { name: 'Create client' }));

    await waitFor(() => getByLabelText(root, 'Client token'));
    expect(root).toHaveTextContent('~/.config/term-llm/config.yaml');
    expect(root.textContent).not.toContain('sk-tlp-multi-secret');
    expect(root.querySelector('pre code')?.textContent).toBe(
      `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
    );

    const copyYAML = getByRole(root, 'button', { name: 'Copy YAML' });
    fireEvent.click(copyYAML);
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(
        `proxy:\n  url: ${location.origin}\n  token: file:///run/secrets/proxy-client-token`,
      ),
    );
    expect(copyYAML.textContent).toBe('Copy YAML');
    expect(toast).toHaveBeenLastCalledWith(
      'YAML copied. Point token at the saved client token.',
      'success',
    );

    const copyWithToken = getByRole(root, 'button', { name: 'Copy with token' });
    fireEvent.click(copyWithToken);
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(
        `proxy:\n  url: ${location.origin}\n  token: 'sk-tlp-multi-secret'`,
      ),
    );
    expect(copyWithToken.textContent).toBe('Copy with token');
    expect(toast).toHaveBeenLastCalledWith(
      'YAML with token copied. Keep config.yaml private.',
      'success',
    );
  });

  it('keeps a legacy partial approval untouched until the operator changes it', async () => {
    const { root, updateProviderApprovals, updateCaller, announce } = setup(
      snapshot({ callers: [caller({ id: 'c1', grants: ['chatgpt'] })] }),
    );
    const row = getByRole(root, 'button', { name: 'Workstation' }).closest('article')!;
    expect(row).toHaveTextContent('chatgpt (needs approval)');
    const editor = openEditor(root);
    const partial = getByLabelText(editor, 'chatgpt (needs approval)') as HTMLInputElement;
    expect(partial.checked).toBe(false);
    expect(partial.indeterminate).toBe(true);
    expect(getByLabelText(editor, 'venice')).not.toBeChecked();

    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    expect(updateProviderApprovals).not.toHaveBeenCalled();
    expect(announce).toHaveBeenLastCalledWith(
      'No provider approval changes to save. Partial approvals are unchanged.',
    );

    // An explicit tick approves every current route in that provider only.
    fireEvent.click(partial);
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    await waitFor(() =>
      expect(updateProviderApprovals).toHaveBeenCalledWith(
        'c1',
        [{ id: 'chatgpt', approved: true }],
        7,
      ),
    );
    expect(updateCaller).not.toHaveBeenCalled();
    expect(announce).toHaveBeenLastCalledWith(
      'Saved provider approvals for Workstation. The token was not changed.',
    );
  });

  it('never widens an untouched partial approval while another provider changes', async () => {
    const { root, updateProviderApprovals } = setup(
      snapshot({ callers: [caller({ id: 'c1', grants: ['chatgpt'] })] }),
    );
    const editor = openEditor(root);
    fireEvent.click(getByLabelText(editor, 'venice'));
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    await waitFor(() =>
      expect(updateProviderApprovals).toHaveBeenCalledWith(
        'c1',
        [{ id: 'venice', approved: true }],
        7,
      ),
    );
  });

  it('revokes every current route of a provider only after confirmation', async () => {
    const { root, updateProviderApprovals, createProviderCaller } = setup(
      snapshot({ callers: [caller({ id: 'c1', grants: ['chatgpt', 'chatgpt-backend'] })] }),
    );
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const editor = openEditor(root);
    fireEvent.click(getByLabelText(editor, 'chatgpt'));
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    expect(confirm).toHaveBeenCalledWith(
      expect.stringContaining('All of its routes (chatgpt, chatgpt-backend)'),
    );
    expect(updateProviderApprovals).not.toHaveBeenCalled();

    confirm.mockReturnValue(true);
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    await waitFor(() =>
      expect(updateProviderApprovals).toHaveBeenCalledWith(
        'c1',
        [{ id: 'chatgpt', approved: false }],
        7,
      ),
    );
    expect(createProviderCaller).not.toHaveBeenCalled();
  });

  it('blocks approving a provider whose shared Claude sign-in is not ready', () => {
    const sharedRoute = { ...route('claude'), account: sharedAccount.name };
    const { root } = setup(
      snapshot({
        routes: [sharedRoute],
        providers: [
          {
            family: 'inference',
            status: 'green',
            reason: '',
            id: 'claude',
            label: 'Claude Code',
            routes: ['claude'],
          },
        ],
        accounts: [sharedAccount],
        callers: [caller({ id: 'c1' })],
      }),
    );
    fireEvent.click(getByRole(root, 'button', { name: 'New client' }));
    const blocked = getByLabelText(root, 'claude (shared subscription not ready)');
    expect(blocked).toBeDisabled();
    expect(getByRole(root, 'button', { name: 'Create client' })).toBeDisabled();
    expect(root).toHaveTextContent(
      'Native providers can be approved once their shared routes are enabled and signed in.',
    );
  });

  it('does not expand a partial approval while the shared subscription is pending', () => {
    const routes = ['claude', 'claude-extra'].map((name) => ({
      ...route(name),
      account: sharedAccount.name,
    }));
    const { root, updateProviderApprovals } = setup(
      snapshot({
        routes,
        providers: [
          {
            family: 'inference',
            status: 'green',
            reason: '',
            id: 'claude',
            label: 'Claude Code',
            routes: routes.map(({ name }) => name),
          },
        ],
        accounts: [sharedAccount],
        callers: [caller({ grants: ['claude'] })],
      }),
    );
    const editor = openEditor(root);
    fireEvent.click(getByLabelText(editor, 'claude (needs approval)'));
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    expect(updateProviderApprovals).not.toHaveBeenCalled();
  });

  it('keeps the native launcher token guard for a provider approval', async () => {
    const claudeRoute = { ...route('claude'), account: 'claude-slot' };
    const account: ProxyAccount = {
      name: 'claude-slot',
      kind: 'claude-bin',
      auth_mode: 'shared_vault',
      state: 'ready',
      generation: 1,
      updated: 1,
    };
    const { root, createProviderCaller } = setup(
      snapshot({
        routes: [claudeRoute],
        providers: [
          {
            family: 'inference',
            status: 'green',
            reason: '',
            id: 'claude',
            label: 'Claude Code',
            routes: ['claude'],
          },
        ],
        accounts: [account],
      }),
    );
    createProviderCaller.mockResolvedValue({
      token: "scope'token",
      caller: caller({ id: 'c_native', name: 'Laptop', grants: ['claude'] }),
    });
    fireEvent.input(getByLabelText(root, 'Client name'), { target: { value: 'Laptop' } });
    fireEvent.click(getByLabelText(root, 'claude'));
    fireEvent.click(getByRole(root, 'button', { name: 'Create client' }));
    const command = await waitFor(
      () => getByLabelText(root, 'Launcher command') as HTMLTextAreaElement,
    );
    expect(command.value).toContain("--token 'scope'\\''token'");
    expect(queryByLabelText(root, 'Operator sign-in command')).toBeNull();
    expect(root).toHaveTextContent('Your normal Claude setup stays unchanged.');
  });

  it('still lets an already approved shared provider be revoked', async () => {
    const sharedRoute = { ...route('claude'), account: sharedAccount.name };
    const { root, updateProviderApprovals } = setup(
      snapshot({
        routes: [sharedRoute],
        providers: [
          {
            family: 'inference',
            status: 'green',
            reason: '',
            id: 'claude',
            label: 'Claude Code',
            routes: ['claude'],
          },
        ],
        accounts: [sharedAccount],
        callers: [caller({ id: 'c1', grants: ['claude'] })],
      }),
    );
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const editor = openEditor(root);
    const approved = getByLabelText(editor, 'claude');
    expect(approved).toBeChecked();
    expect(approved).toBeEnabled();
    fireEvent.click(approved);
    fireEvent.click(getByRole(root, 'button', { name: 'Save approvals' }));
    await waitFor(() =>
      expect(updateProviderApprovals).toHaveBeenCalledWith(
        'c1',
        [{ id: 'claude', approved: false }],
        7,
      ),
    );
  });
});
