import { afterEach, expect, it, vi } from 'vitest';
import {
  fireEvent,
  getAllByRole,
  getByLabelText,
  getByRole,
  getByText,
  queryByRole,
  waitFor,
} from '@testing-library/preact';
import { ProxyClient, type ProxyAdminRoute, type ProxyState } from '../src/api/proxy-client';
import type { UIContext } from './dom';
import { credentialSummary } from './provider-detail';
import { providerGroups } from './provider-groups';
import { openProvider, providerItem } from './provider-test-helpers';
import { routesView } from './routes';

const auth = { header: 'Authorization', prefix: 'Bearer ' };
const route = (name: string, upstream: string): ProxyAdminRoute => ({
  serves: [],
  profile: '',
  provider: name,
  name,
  local_prefix: `/${name}/`,
  upstream,
  caller_auth: auth,
  enabled: true,
  allow_insecure_http: false,
  set_headers: {},
});
const chat = {
  ...route('chatgpt', 'https://chatgpt.com/backend-api/codex'),
  profile: 'chatgpt',
  account: 'personal',
};
const usage = {
  ...route('chatgpt-backend', 'https://chatgpt.com/backend-api'),
  account: 'personal',
};
const media = { ...route('chatgpt-openai', 'https://api.openai.com/v1'), account: 'personal' };
const openai = route('openai', media.upstream);
const preset = (route: ProxyAdminRoute, label: string, account_kind?: 'chatgpt') => ({
  family: 'inference' as const,
  id: route.name,
  label,
  upstream: route.upstream,
  auth,
  account_kind,
});

function snapshot(overrides: Partial<ProxyState> = {}): ProxyState {
  return {
    defaults: null,
    auth: 'token',
    mode: 'ui',
    revision: 7,
    csrf: 'csrf',
    admin_keys: [],
    accounts: [{ name: 'personal', kind: 'chatgpt', state: 'ready', generation: 1, updated: 0 }],
    secrets: [],
    routes: [chat, usage, media, openai],
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'chatgpt',
        label: 'ChatGPT',
        routes: [chat.name, usage.name, media.name],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'openai',
        label: 'OpenAI',
        routes: [openai.name],
      },
    ],
    presets: [
      preset(chat, 'ChatGPT', 'chatgpt'),
      preset(usage, 'ChatGPT Usage & Dictation', 'chatgpt'),
      preset(media, 'ChatGPT Embeddings & Live', 'chatgpt'),
      preset(openai, 'OpenAI'),
    ],
    callers: [
      {
        id: 'a',
        name: 'jarvis',
        created: 1,
        revoked: false,
        grants: [chat.name],
        created_by: '',
        external_id: '',
      },
      {
        id: 'b',
        name: 'revoked',
        created: 1,
        revoked: true,
        grants: [usage.name],
        created_by: '',
        external_id: '',
      },
    ],
    ...overrides,
  };
}

function fixture(state = snapshot()) {
  const api = new ProxyClient(vi.fn());
  const config = vi.spyOn(api, 'config').mockResolvedValue();
  const context: UIContext = {
    run: async (operation) => {
      await operation();
      return true;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
    toast: vi.fn(),
    announce: vi.fn(),
    navigate: vi.fn(),
    protectToken: vi.fn(),
  };
  const root = routesView(state, api, context);
  document.body.append(root);
  return { root, config, context };
}

afterEach(() => document.body.replaceChildren());

it('lists one row per provider and shows only the selected provider’s detail', () => {
  const { root } = fixture();
  const items = root.querySelectorAll<HTMLElement>('.provider-list-item');
  expect([...items].map((item) => item.dataset.provider)).toEqual(['chatgpt', 'openai']);
  const chatItem = getByRole(root, 'button', {
    name: 'chatgpt (ChatGPT), 1 client with access, 1 needs approval',
  });
  // The partial grant is the actionable note; the revoked client is ignored.
  expect(chatItem.querySelector('.provider-list-note')).toHaveTextContent(
    '1 client needs approval',
  );
  expect(chatItem).toHaveAttribute('aria-current', 'true');
  expect(
    getByRole(root, 'button', { name: 'openai (OpenAI), 0 clients with access' }),
  ).not.toHaveAttribute('aria-current');
  expect(root.querySelectorAll('.provider-detail')).toHaveLength(1);
  const detail = getByRole(root, 'region', { name: 'chatgpt (ChatGPT)' });
  expect(getByRole(detail, 'heading', { name: 'chatgpt' })).toBeVisible();
  expect(
    [...detail.querySelectorAll('.provider-detail-routes code')].map((n) => n.textContent),
  ).toEqual(['/chatgpt/', '/chatgpt-backend/', '/chatgpt-openai/']);
  expect(
    getAllByRole(detail, 'tab').map((tab) => [tab.textContent, tab.getAttribute('aria-selected')]),
  ).toEqual([
    ['Access 1', 'true'],
    ['Connection', 'false'],
    ['Client config', 'false'],
    ['Settings', 'false'],
  ]);
  expect(getByRole(detail, 'tabpanel')).toHaveTextContent('jarvis');
  expect(getByRole(detail, 'tabpanel')).not.toHaveTextContent('revoked');
  // Connection, including the shared account controls, stays mounted but hidden.
  expect(detail.querySelectorAll('.account-controls')).toHaveLength(1);
  expect(detail.querySelector('[aria-label="Edit chatgpt"]')).not.toBeVisible();
  openProvider(root, chat.name, 'connection');
  expect(getByRole(detail, 'button', { name: 'Edit chatgpt' })).toBeVisible();
});

it('renames a provider from Settings without repeating an unchanged label', async () => {
  const state = snapshot();
  state.providers[1].label = 'openai';
  const api = new ProxyClient(vi.fn());
  const rename = vi.spyOn(api, 'renameProviderLabel').mockResolvedValue({ revision: 8 });
  const context: UIContext = {
    run: async (operation) => {
      await operation();
      return true;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
    toast: vi.fn(),
    announce: vi.fn(),
    navigate: vi.fn(),
    protectToken: vi.fn(),
  };
  const root = routesView(state, api, context);
  document.body.append(root);
  const detail = openProvider(root, openai.name, 'settings');
  expect(detail.querySelector('.provider-detail-title')!.textContent).not.toContain('openaiopenai');
  const label = getByLabelText(detail, 'Provider label') as HTMLInputElement;
  expect(label).toBeVisible();
  expect(label).toHaveValue('openai');
  fireEvent.input(label, { target: { value: 'Frontier' } });
  fireEvent.click(getByRole(detail, 'button', { name: 'Save label' }));
  await waitFor(() => expect(rename).toHaveBeenCalledWith(7, 'openai', 'Frontier'));
});

it('adds a missing companion to its provider without changing other routes or approvals', async () => {
  const state = snapshot({ routes: [chat, openai] });
  state.providers[0].routes = [chat.name];
  state.presets![0].companions = [usage.name];
  const { root, config, context } = fixture(state);
  const detail = openProvider(root, chat.name, 'connection');
  fireEvent.click(getByRole(detail, 'button', { name: 'Add ChatGPT Usage & Dictation route' }));
  const form = detail.querySelector<HTMLElement>('.route-form')!;
  expect(getByRole(detail, 'heading', { name: 'Set up ChatGPT Usage & Dictation' })).toBeVisible();
  expect(getByLabelText(form, 'Name')).toHaveValue(usage.name);
  expect(getByLabelText(form, 'Trusted account')).toHaveValue(chat.account);
  fireEvent.click(getByRole(form, 'button', { name: 'Save route' }));
  await waitFor(() => expect(config).toHaveBeenCalledOnce());
  const [loaded, routes] = config.mock.calls[0];
  expect(loaded).toBe(state);
  expect(routes.slice(0, 2)).toEqual([chat, openai]);
  expect(routes[2]).toMatchObject({
    name: usage.name,
    profile: usage.name,
    provider: 'chatgpt',
    account: chat.account,
  });
  expect(context.refresh).toHaveBeenCalledWith(`route:${usage.name}`);
});

it('keeps the selected provider and tab across refreshes and closes editors on switch', () => {
  const state = snapshot();
  const { root, context } = fixture(state);
  openProvider(root, openai.name, 'connection');
  fireEvent.click(getByRole(root, 'button', { name: 'Edit openai' }));
  expect(root.querySelector('.route-form')).toBeVisible();
  const next = routesView(state, new ProxyClient(vi.fn()), context);
  document.body.replaceChildren(next);
  expect(next.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'openai');
  expect(getByRole(next, 'tab', { selected: true })).toHaveTextContent('Connection');
  fireEvent.click(getByRole(next, 'button', { name: 'Edit openai' }));
  providerItem(next, chat.name).click();
  expect(next.querySelector('.route-form')).toBeNull();
  expect(next.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'chatgpt');
  expect(getByRole(next, 'tab', { selected: true })).toHaveTextContent('Connection');
});

it('refuses to switch provider or tab while a one-time token is displayed', () => {
  const state = snapshot();
  const { root, context } = fixture(state);
  context.hasProtectedToken = () => true;
  providerItem(root, openai.name).click();
  expect(root.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'chatgpt');
  fireEvent.click(getByRole(root, 'tab', { name: 'Connection' }));
  expect(getByRole(root, 'tab', { selected: true })).toHaveTextContent('Access');
  expect(context.announce).toHaveBeenCalledWith(
    'Finish saving or dismissing the one-time connection details first.',
  );
});

it('moves between tabs with arrow, Home and End keys', () => {
  const { root } = fixture();
  const access = getByRole(root, 'tab', { name: 'Access 1' });
  access.focus();
  // Modified keys (Alt+Left is Back) belong to the browser.
  fireEvent.keyDown(access, { key: 'ArrowRight', altKey: true });
  expect(access).toHaveAttribute('aria-selected', 'true');
  // Panels are reachable even when they hold no controls (read-only mode).
  for (const panel of root.querySelectorAll('[role="tabpanel"]'))
    expect(panel).toHaveAttribute('tabindex', '0');
  fireEvent.keyDown(access, { key: 'ArrowRight' });
  const connection = getByRole(root, 'tab', { name: 'Connection' });
  expect(connection).toHaveFocus();
  expect(connection).toHaveAttribute('aria-selected', 'true');
  expect(connection).toHaveAttribute('tabindex', '0');
  expect(access).toHaveAttribute('tabindex', '-1');
  fireEvent.keyDown(connection, { key: 'End' });
  expect(getByRole(root, 'tab', { name: 'Settings' })).toHaveFocus();
  fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' });
  expect(access).toHaveFocus();
  fireEvent.keyDown(access, { key: 'ArrowLeft' });
  expect(getByRole(root, 'tab', { name: 'Settings' })).toHaveFocus();
  fireEvent.keyDown(document.activeElement!, { key: 'Home' });
  expect(access).toHaveAttribute('aria-selected', 'true');
  expect(getByRole(root, 'tabpanel')).toBe(
    document.getElementById(access.getAttribute('aria-controls')!),
  );
});

it('renders only providers and routes listed by the server', () => {
  const groups = providerGroups(
    snapshot({
      providers: [
        {
          family: 'inference',
          status: 'green',
          reason: '',
          id: 'chatgpt',
          label: 'ChatGPT',
          routes: [chat.name],
        },
      ],
    }),
  );
  expect(groups.map((group) => group.routes.map(({ name }) => name))).toEqual([[chat.name]]);
});

it('groups providers by server family with status and reason, and filters long lists', () => {
  const inference = Array.from({ length: 11 }, (_, index) => {
    const id = `infer-${index + 1}`;
    return { ...route(id, openai.upstream), serves: [`providers.${id}.base_url`] };
  });
  const exa = {
    ...route('exa-mcp', 'https://mcp.exa.ai'),
    provider: 'exa',
    profile: 'exa-mcp',
    serves: ['search: exa_mcp'],
  };
  const jina = { ...route('jina', 'https://api.jina.ai'), serves: ['embed: jina'] };
  const groups: ProxyState['providers'] = [
    ...inference
      .map(
        (r) =>
          ({
            id: r.provider,
            label: r.name,
            routes: [r.name],
            family: 'inference',
            status: 'green',
            reason: '',
          }) as const,
      )
      .map((p) => ({ ...p, routes: [...p.routes] })),
    {
      id: 'exa',
      label: 'Exa',
      family: 'search',
      status: 'red',
      reason: 'Sign-in needed',
      routes: [exa.name],
    },
    {
      id: 'jina',
      label: 'Jina',
      family: 'embeddings',
      status: 'orange',
      reason: 'Partly disabled',
      routes: [jina.name],
    },
  ];
  const { root } = fixture(snapshot({ routes: [...inference, exa, jina], providers: groups }));
  const list = getByRole(root, 'navigation', { name: 'Providers' });
  expect(getAllByRole(list, 'heading').map((heading) => heading.textContent)).toEqual([
    'Inference',
    'Search',
    'Embeddings',
  ]);
  // A compact list needs no pagination: every provider has a row.
  expect(getByRole(list, 'list', { name: 'Inference' }).querySelectorAll('li')).toHaveLength(11);
  const exaItem = providerItem(root, exa.name);
  expect(exaItem).toHaveAccessibleName('exa (Exa), Sign-in needed, 0 clients with access');
  expect(exaItem.querySelector('.provider-list-note')).toHaveClass('provider-list-note-red');
  // The row's accessible name carries the reason; its dot is decorative.
  expect(exaItem.querySelector('.provider-dot')).toHaveClass('provider-dot-red');
  expect(exaItem.querySelector('.provider-dot')).toHaveAttribute('aria-hidden', 'true');
  const detail = openProvider(root, exa.name, 'connection');
  expect(getByRole(detail, 'heading', { name: 'exa' })).toBeVisible();
  expect(detail.querySelector('.provider-status')).toHaveTextContent('Sign-in needed');
  expect(detail.querySelector('.provider-detail-meta')).toHaveTextContent('Search');
  expect(detail.querySelector('[data-focus="route:exa-mcp"]')).toHaveTextContent('/exa-mcp/');
  expect(
    getByRole(detail, 'tab', { name: /Connection/ }).querySelector('.provider-tab-mark-down'),
  ).not.toBeNull();
  expect(queryByRole(detail, 'tab', { name: 'Client config' })).toBeNull();
  expect(detail.querySelector('.route-facts')).toHaveTextContent(
    'term-llm uses it forsearch: exa_mcp',
  );

  const filter = getByRole(list, 'searchbox', { name: 'Filter providers' });
  fireEvent.input(filter, { target: { value: 'EXA' } });
  expect(exaItem.closest('li')).toBeVisible();
  expect(providerItem(root, jina.name).closest('li')).not.toBeVisible();
  expect(getByRole(list, 'heading', { name: 'Inference', hidden: true })).not.toBeVisible();
  fireEvent.input(filter, { target: { value: 'nothing' } });
  expect(getByText(list, 'No providers match.')).toBeVisible();
});

it('summarizes how the proxy authenticates upstream for each provider', () => {
  const keyed = {
    ...openai,
    set_headers: { Authorization: { secret: 'openai-key', prefix: 'Bearer ' } },
  };
  const literal = { ...openai, set_headers: { 'X-Project': { value: 'public' } } };
  const state = snapshot({
    routes: [chat, usage, media, keyed],
    secrets: [{ name: 'openai-key', source: 'stored', routes: null }],
  });
  const [chatGroup, openaiGroup] = providerGroups(state);
  expect(credentialSummary(chatGroup, state)).toBe('Account personal');
  expect(credentialSummary(openaiGroup, state)).toBe('API key openai-key');
  expect(credentialSummary(openaiGroup, { ...state, secrets: [] })).toBe('API key missing');
  const plain = providerGroups({ ...state, routes: [chat, usage, media, literal] })[1];
  expect(credentialSummary(plain, state)).toBe('No upstream credential');
});

it('defaults to the first listed provider and keeps the selection on a plain refresh', () => {
  const search = { ...openai, name: 'a-search', provider: 'a-search', local_prefix: '/a/' };
  const state = snapshot({
    routes: [search, openai],
    providers: [
      {
        family: 'search',
        status: 'green',
        reason: '',
        id: 'a-search',
        label: 'a-search',
        routes: [search.name],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'openai',
        label: 'openai',
        routes: [openai.name],
      },
    ],
  });
  const { root, context } = fixture(state);
  // The list groups Inference before Search, so its first row is selected.
  expect(root.querySelector('.provider-list-item')).toHaveAttribute('data-provider', 'openai');
  expect(root.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'openai');
  // A provider added elsewhere appears on refresh without taking over the view.
  const media = { ...openai, name: 'x-media', provider: 'x-media', local_prefix: '/x/' };
  const next = routesView(
    {
      ...state,
      routes: [...state.routes, media],
      providers: [
        ...state.providers,
        {
          family: 'media',
          status: 'green',
          reason: '',
          id: 'x-media',
          label: 'x-media',
          routes: [media.name],
        },
      ],
    },
    new ProxyClient(vi.fn()),
    context,
  );
  expect(next.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'openai');
});

it('moves the selection only for a refresh that names a route, then opens Access', () => {
  const routes = Array.from({ length: 8 }, (_, index) => ({
    ...route(`infer-${index + 1}`, openai.upstream),
  }));
  const state = snapshot({
    routes,
    providers: routes.map((r) => ({
      id: r.name,
      label: r.name,
      family: 'inference' as const,
      status: 'green' as const,
      reason: '',
      routes: [r.name],
    })),
  });
  const { root, context } = fixture(state);
  const api = new ProxyClient(vi.fn());
  const render = (focusKey?: string) => {
    const view = routesView(state, api, context, focusKey);
    document.body.replaceChildren(view);
    return view;
  };
  openProvider(root, 'infer-2', 'settings');
  fireEvent.input(getByRole(root, 'searchbox', { name: 'Filter providers' }), {
    target: { value: 'infer-2' },
  });
  // Plain refreshes and unrelated focus keys keep the operator where they were.
  for (const key of [undefined, 'client-defaults', 'caller:a', 'secret:infer-5', 'infer-5']) {
    const view = render(key);
    expect(view.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'infer-2');
    expect(getByRole(view, 'tab', { selected: true })).toHaveTextContent('Settings');
    expect(getByRole(view, 'searchbox', { name: 'Filter providers' })).toHaveValue('infer-2');
  }
  // A save of another provider's route selects it on Access and drops the hiding filter.
  let view = render('route:infer-5');
  expect(view.querySelector('.provider-detail')).toHaveAttribute('data-provider', 'infer-5');
  expect(getByRole(view, 'tab', { selected: true })).toHaveTextContent('Access');
  expect(getByRole(view, 'searchbox', { name: 'Filter providers' })).toHaveValue('');
  // Saving the selected provider's own route keeps its tab.
  openProvider(view, 'infer-5', 'connection');
  view = render('route:infer-5');
  expect(getByRole(view, 'tab', { selected: true })).toHaveTextContent('Connection');
});
