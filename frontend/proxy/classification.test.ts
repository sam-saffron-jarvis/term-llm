import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, getByLabelText, getByRole, waitFor, within } from '@testing-library/preact';
import {
  ProxyClient,
  type ProxyAdminRoute,
  type ProxyPreset,
  type ProxyState,
} from '../src/api/proxy-client';
import type { UIContext } from './dom';
import { allowLabel } from './route-rules';
import { openProvider } from './provider-test-helpers';
import { routesView } from './routes';

const auth = { header: 'Authorization', prefix: 'Bearer ' };

const presets: ProxyPreset[] = [
  {
    family: 'classification',
    id: 'typesafe',
    label: 'TypeSafe',
    upstream: 'https://api.typesafe.ai',
    auth,
    billing: 'unspecified',
    usage: 'off',
    allow: ['POST =/v1/systemone', 'GET =/v1/models'],
    note: "Use this route's local prefix as the classifier base URL, without adding /v1.",
  },
  {
    family: 'classification',
    id: 'openai-decisions',
    label: 'OpenAI Decisions',
    upstream: 'https://api.openai.com/v1',
    auth,
    billing: 'unspecified',
    usage: 'off',
    allow: ['POST =/decisions'],
    note: 'This route serves Decisions only.',
  },
  {
    family: 'classification',
    id: 'cloudflare-clef',
    label: 'Cloudflare Clef',
    upstream: '',
    auth,
    billing: 'unspecified',
    usage: 'off',
    allow: ['POST =/@cf/cloudflare/clef', 'POST =/@cf/cloudflare/clef-flash'],
  },
];

const typesafeRoute: ProxyAdminRoute = {
  serves: [
    'classify.providers.typesafe.base_url',
    'classify.providers.typesafe.api_key',
    'classify: typesafe',
  ],
  profile: 'typesafe',
  provider: 'typesafe',
  name: 'typesafe',
  local_prefix: '/typesafe/',
  upstream: 'https://api.typesafe.ai',
  enabled: true,
  allow_insecure_http: false,
  caller_auth: auth,
  set_headers: { Authorization: { secret: 'typesafe', prefix: 'Bearer ' } },
  billing: 'unspecified',
  usage: 'off',
  allow: ['POST =/v1/systemone', 'GET =/v1/models'],
};

function setup(overrides: Partial<ProxyState> = {}) {
  const api = new ProxyClient(vi.fn());
  const create = vi.spyOn(api, 'createProvider').mockResolvedValue({ revision: 8 });
  const config = vi.spyOn(api, 'config').mockResolvedValue();
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
    toast: vi.fn(),
    announce,
    refresh: vi.fn(),
    protectToken: vi.fn(),
    navigate: vi.fn(),
  };
  const state: ProxyState = {
    defaults: null,
    providers: [],
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: [],
    secrets: [],
    callers: [],
    admin_keys: [],
    csrf: 'csrf-token',
    presets,
    ...overrides,
  };
  const root = routesView(state, api, context);
  document.body.append(root);
  return { root, create, config, announce };
}

function openPicker(root: HTMLElement) {
  fireEvent.click(getByRole(root, 'button', { name: 'Add provider' }));
  return root.querySelector<HTMLElement>('.route-picker')!;
}

function choose(root: HTMLElement, label: string) {
  const picker = openPicker(root);
  fireEvent.click(getByRole(picker, 'button', { name: label }));
}

function fixtureState(overrides: Partial<ProxyState> = {}): ProxyState {
  return {
    defaults: null,
    providers: [
      {
        id: 'typesafe',
        label: 'TypeSafe',
        family: 'classification',
        status: 'green',
        reason: '',
        routes: ['typesafe'],
      },
    ],
    auth: 'token',
    mode: 'ui',
    revision: 7,
    routes: [typesafeRoute],
    secrets: [{ name: 'typesafe', source: 'stored', routes: ['typesafe'] }],
    callers: [],
    admin_keys: [],
    csrf: 'csrf-token',
    presets,
    ...overrides,
  };
}

afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

it('groups classification presets and finds TypeSafe by searching for Jev', () => {
  const { root } = setup();
  const picker = openPicker(root);
  expect(getByRole(picker, 'region', { name: 'Classification' })).toBeVisible();
  expect(
    [...picker.querySelectorAll('.route-preset-family h3')].map((node) => node.textContent),
  ).toEqual(['Classification']);
  expect(getByRole(picker, 'button', { name: 'TypeSafe' })).toBeVisible();
  expect(getByRole(picker, 'button', { name: 'OpenAI Decisions' })).toBeVisible();
  expect(getByRole(picker, 'button', { name: 'Cloudflare Clef' })).toBeVisible();

  const search = getByRole(picker, 'searchbox', { name: 'Search providers' });
  fireEvent.input(search, { target: { value: 'Jev' } });
  expect(getByRole(picker, 'button', { name: 'TypeSafe' })).toBeVisible();
  expect(picker.querySelector('[aria-label="OpenAI Decisions"]')).not.toBeVisible();
  fireEvent.keyDown(search, { key: 'Enter' });

  const name = getByLabelText(root, 'Provider name');
  expect(name).toHaveValue('typesafe');
  expect(name).toHaveAttribute('readonly');
  expect(root.querySelector('.provider-config-create')).toBeNull();
  expect(root).not.toHaveTextContent('Client config (term-llm)');
});

it('creates a classifier with a saved bearer secret and no inference config', async () => {
  const { root, create } = setup();
  const picker = openPicker(root);
  fireEvent.input(getByRole(picker, 'searchbox', { name: 'Search providers' }), {
    target: { value: 'jev' },
  });
  fireEvent.keyDown(getByRole(picker, 'searchbox', { name: 'Search providers' }), {
    key: 'Enter',
  });

  expect(getByLabelText(root, 'Credential source')).toHaveValue('new');
  expect(root.querySelectorAll('input[type=password]')).toHaveLength(1);
  fireEvent.input(getByLabelText(root, 'API key'), { target: { value: 'synthetic-key' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create provider' }));

  await waitFor(() =>
    expect(create).toHaveBeenCalledWith(7, {
      name: 'typesafe',
      label: 'TypeSafe',
      preset: 'typesafe',
      value: 'synthetic-key',
    }),
  );
  expect(getByLabelText(root, 'API key')).toHaveValue('');
});

it('requires an operator-provided root for a blank classification upstream', async () => {
  const { root, create } = setup();
  choose(root, 'Cloudflare Clef');
  const upstream = getByLabelText(root, 'Upstream base URL');
  expect(upstream).toHaveValue('');
  expect(upstream).toBeRequired();
  expect(upstream).toHaveAccessibleDescription(/Workers AI run root/);
  expect(upstream).toHaveAccessibleDescription(/AI Gateway Workers AI root/);
  expect(getByLabelText(root, 'Credential source')).toHaveValue('new');

  const rootURL = 'https://api.cloudflare.com/client/v4/accounts/account-id/ai/run';
  fireEvent.input(upstream, { target: { value: rootURL } });
  fireEvent.input(getByLabelText(root, 'API key'), { target: { value: 'synthetic-key' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Create provider' }));

  await waitFor(() =>
    expect(create).toHaveBeenCalledWith(7, {
      name: 'cloudflare-clef',
      label: 'Cloudflare Clef',
      preset: 'cloudflare-clef',
      value: 'synthetic-key',
      routes: [{ name: 'cloudflare-clef', profile: 'cloudflare-clef', upstream: rootURL }],
    }),
  );
});

it('renders a Classification provider section without client inference config', () => {
  const { root } = setup(fixtureState());
  const list = getByRole(root, 'navigation', { name: 'Providers' });
  expect(within(list).getByRole('heading', { name: 'Classification' })).toBeVisible();
  const section = within(list).getByRole('list', { name: 'Classification' });
  expect(within(section).getByRole('button', { name: /^typesafe \(TypeSafe\)/ })).toBeVisible();
  const detail = openProvider(root, 'typesafe', 'connection');
  expect(within(detail).getByRole('heading', { name: 'typesafe' })).toBeVisible();
  expect(within(detail).queryByRole('tab', { name: 'Client config' })).toBeNull();
  expect(
    within(detail).queryByRole('heading', { name: 'Client config (term-llm)', hidden: true }),
  ).toBeNull();
  expect(within(detail).queryByRole('button', { name: /client config/i, hidden: true })).toBeNull();

  const settings = root.querySelector<HTMLElement>('.route-row-settings')!;
  expect(settings).toHaveTextContent('POST exactly /v1/systemone');
  expect(settings).toHaveTextContent('GET exactly /v1/models');

  const defaults = root.querySelector<HTMLElement>('[data-focus="client-defaults"]')!;
  fireEvent.click(within(defaults).getByRole('button', { name: 'Edit' }));
  const classifier = within(defaults).getByLabelText('Classification');
  expect(within(classifier).getByRole('option', { name: 'typesafe' })).toHaveValue('typesafe');
});

it('keeps exact allow entries in the editor and saves them unchanged', async () => {
  const { root, config } = setup(fixtureState());
  openProvider(root, 'typesafe', 'connection');
  fireEvent.click(getByRole(root, 'button', { name: 'Edit typesafe' }));
  const allow = getByLabelText(root, 'Allowed requests');
  expect(allow).toHaveValue('POST =/v1/systemone\nGET =/v1/models');
  expect(allow).toHaveAccessibleDescription(/METHOD =\/path matches only that exact path/);
  fireEvent.input(allow, { target: { value: 'POST =/v1/systemone' } });
  fireEvent.click(getByRole(root, 'button', { name: 'Save route' }));
  await waitFor(() => expect(config).toHaveBeenCalledOnce());
  expect(config.mock.calls[0][1][0].allow).toEqual(['POST =/v1/systemone']);
});

it('labels exact and prefix allow rules distinctly', () => {
  expect(allowLabel('POST =/v1/systemone')).toBe('POST exactly /v1/systemone');
  expect(allowLabel('GET =/v1/models')).toBe('GET exactly /v1/models');
  expect(allowLabel('POST =/@cf/cloudflare/clef')).toBe('POST exactly /@cf/cloudflare/clef');
  expect(allowLabel('GET /v1/*')).toBe('GET any path below /v1/');
  expect(allowLabel('HEAD /*')).toBe('HEAD any path');
  expect(allowLabel('POST /models')).toBe('POST /models');
  expect(allowLabel('not a rule')).toBe('not a rule');
});
