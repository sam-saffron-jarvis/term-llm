import { withSingletonProviders } from '../proxy/test-fixtures';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { gunzipSync } from 'node:zlib';
import { expect, test, type Locator, type Page, type Request } from '@playwright/test';
import type { ProxyState } from '../src/api/proxy-client';

// Exercise the built standalone bundle without a server, credentials, or upstreams.
const dist =
  process.env.TERM_LLM_PROXY_DIST ??
  resolve(import.meta.dirname, '../../internal/serveui/static/dist');
function asset(name: string): Buffer {
  try {
    return readFileSync(resolve(dist, name));
  } catch {
    return gunzipSync(readFileSync(resolve(dist, `${name}.gz`)));
  }
}
const javascript = asset('proxy.js');
const stylesheet = asset('proxy.css');
const snapshot: ProxyState = {
  defaults: null,
  auth: 'passkey',
  mode: 'ui',
  revision: 1,
  routes: [],
  providers: [],
  secrets: [],
  callers: [],
  admin_keys: [],
  csrf: 'synthetic-layout-csrf',
};

test.beforeEach(async ({ page }) => {
  await page.route('http://proxy.test/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/_proxy/assets/proxy.js')
      return route.fulfill({ contentType: 'text/javascript', body: javascript });
    if (path === '/_proxy/assets/proxy.css')
      return route.fulfill({ contentType: 'text/css; charset=utf-8', body: stylesheet });
    if (path === '/_proxy/api/auth/status')
      return route.fulfill({ json: { auth: 'passkey', origin: 'http://proxy.test' } });
    if (path === '/_proxy/api/state') return route.fulfill({ json: snapshot });
    if (path === '/_proxy/')
      return route.fulfill({
        contentType: 'text/html',
        body: '<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/_proxy/assets/proxy.css"></head><body><div id="proxy-app"></div><script type="module" src="/_proxy/assets/proxy.js"></script></body></html>',
      });
    return route.abort();
  });
});

async function choosePreset(page: Page, name: string) {
  await page.getByRole('button', { name: 'Add provider', exact: true }).click();
  const picker = page.locator('.route-picker');
  await picker.getByRole('button', { name, exact: true }).click();
}

function providerItem(page: Page, routeName: string): Locator {
  return page.locator(`.provider-list-item[data-routes~="${routeName}"]`);
}

function providerRoutePrefix(detail: Locator, routeName: string): Locator {
  return detail.locator(`.provider-detail-routes code[data-focus="route:${routeName}"]`);
}

type ProviderTab = 'access' | 'connection' | 'config' | 'settings';

function providerTab(detail: Locator, tab: ProviderTab): Locator {
  return detail.locator(`[role="tab"][data-tab="${tab}"]`);
}

async function openProvider(page: Page, routeName: string, tab: ProviderTab = 'access') {
  const item = providerItem(page, routeName);
  await expect(item).toHaveAttribute('data-provider', /.+/);
  const id = await item.getAttribute('data-provider');
  await item.click();
  await expect(item).toHaveAttribute('aria-current', 'true');
  const detail = page.locator(`#provider-detail[data-provider="${id}"]`);
  await expect(detail).toBeVisible();
  const control = providerTab(detail, tab);
  await control.click();
  await expect(control).toHaveAttribute('aria-selected', 'true');
  return detail;
}

function routeFact(route: Locator, label: RegExp): Locator {
  return route.locator('.route-facts dt').filter({ hasText: label }).locator('+ dd');
}

/**
 * Wait for a ledger list request carrying the given query. The ledger polls
 * page one every second with the applied filters, so waiting for the next
 * `/api/requests` call alone can capture a poll instead of the control under
 * test.
 */
function waitForLedgerRequest(
  page: Page,
  expected: (params: URLSearchParams) => boolean,
): Promise<Request> {
  return page.waitForRequest((request) => {
    const url = new URL(request.url());
    return url.pathname === '/_proxy/api/requests' && expected(url.searchParams);
  });
}

function requestQuery(request: Request): URLSearchParams {
  return new URL(request.url()).searchParams;
}

function expectQuery(request: Request, expected: Record<string, string>): void {
  expect(Object.fromEntries(requestQuery(request))).toEqual(expected);
}

for (const scheme of ['light', 'dark'] as const) {
  test(`proxy layout aligns preset actions with ${scheme} OS preference`, async ({
    page,
  }, testInfo) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await expect(page.getByRole('heading', { name: 'term-llm Proxy' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add provider', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Add provider', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Add a provider' })).toBeFocused();
    // The standalone proxy deliberately keeps its existing light palette.
    await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(255, 255, 255)');
    const buttons = await page
      .locator('.route-preset-choice, .route-preset-custom')
      .evaluateAll((items) =>
        items.map((item) => {
          const rect = item.getBoundingClientRect();
          return { x: rect.x, width: rect.width };
        }),
      );
    expect(buttons).toHaveLength(1);
    for (const button of buttons) {
      expect(button.x).toBeCloseTo(buttons[0].x, 0);
      if (page.viewportSize()!.width > 640) expect(button.width).toBeCloseTo(buttons[0].width, 0);
    }
    await page.screenshot({ path: testInfo.outputPath(`proxy-${scheme}.png`), fullPage: true });
    await page.getByRole('button', { name: 'Custom endpoint', exact: true }).click();
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toBeVisible();
    const name = page.getByRole('textbox', { name: 'Name', exact: true });
    await expect(name).toHaveAttribute('data-1p-ignore', 'true');
    await expect(name).toHaveAttribute('autocomplete', 'off');
    await expect(name).toBeFocused();
    await page.getByRole('textbox', { name: 'Upstream base URL', exact: false }).focus();
    await expect(name).not.toBeFocused();
    for (const control of await page.locator('.route-form input, .route-form select').all()) {
      if (!(await control.isVisible())) continue;
      await expect(control).toHaveCSS('border-top-style', 'solid');
      await expect(control).toHaveCSS('border-top-width', '1px');
      await expect(control).not.toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)');
    }
    await page.screenshot({
      path: testInfo.outputPath(`proxy-form-${scheme}.png`),
      fullPage: true,
    });

    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
  });
}

test('proxy mobile layout keeps navigation and the preset picker usable at narrow widths', async ({
  page,
}, testInfo) => {
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await page.getByRole('button', { name: 'Add provider', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Custom endpoint', exact: true })).toBeVisible();
    const lastTab = await page.getByRole('button', { name: 'Passkeys', exact: true }).boundingBox();
    expect(lastTab!.x + lastTab!.width).toBeLessThanOrEqual(width);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
    const picker = (await page.locator('.route-picker').boundingBox())!;
    const choice = (await page
      .getByRole('button', { name: 'Custom endpoint', exact: true })
      .boundingBox())!;
    expect(choice.x).toBeGreaterThanOrEqual(picker.x);
    expect(choice.x + choice.width).toBeLessThanOrEqual(picker.x + picker.width);
    await page.screenshot({
      path: testInfo.outputPath(`proxy-mobile-${width}.png`),
      fullPage: true,
    });
    await page.getByRole('button', { name: 'Custom endpoint', exact: true }).click();
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
  }
});

test('Admin separates configuration download from replacement without nested disclosures', async ({
  page,
}, testInfo) => {
  const yaml = 'routes: []\n';
  await page.route('http://proxy.test/_proxy/api/export', (route) =>
    route.fulfill({ contentType: 'text/yaml', body: yaml }),
  );
  await page.route('http://proxy.test/_proxy/api/import', (route) =>
    route.fulfill({ json: { ok: true } }),
  );
  await page.goto('http://proxy.test/_proxy/?tab=admin');
  await expect(page.getByRole('heading', { name: 'Admin', exact: true })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Download configuration', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Replace route configuration', exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/not a full backup/i)).toBeVisible();
  await expect(page.getByLabel('YAML file', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Route configuration YAML')).not.toBeVisible();
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download routes.yaml' }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe('routes.yaml');
  expect(readFileSync((await download.path())!, 'utf8')).toBe(yaml);
  await expect(page.locator('.toast-region').getByRole('status')).toContainText(
    'Route configuration downloaded',
  );
  await page.getByLabel('Import source', { exact: true }).selectOption('paste');
  await expect(page.getByLabel('Route configuration YAML')).toBeVisible();
  await expect(page.getByLabel('YAML file', { exact: true })).not.toBeVisible();
  await page.getByLabel('Import source', { exact: true }).selectOption('file');
  await page.getByLabel('YAML file', { exact: true }).setInputFiles({
    name: 'routes.yaml',
    mimeType: 'text/yaml',
    buffer: Buffer.from(yaml),
  });
  await page.screenshot({ path: testInfo.outputPath('configuration-file.png'), fullPage: true });
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('lose client grants');
    await dialog.accept();
  });
  const replacement = page.waitForRequest('http://proxy.test/_proxy/api/import');
  await page.getByRole('button', { name: 'Replace routes from YAML' }).click();
  expect((await replacement).postDataJSON()).toEqual({ revision: 1, yaml });
  await expect(page.locator('#proxy-app > [role="status"]')).toContainText(
    'Route configuration replaced',
  );
});

for (const [id, label, upstream, header, prefix] of [
  ['openai', 'OpenAI', 'https://api.openai.com/v1', 'Authorization', 'Bearer '],
  ['anthropic', 'Anthropic', 'https://api.anthropic.com', 'x-api-key', ''],
]) {
  test(`${label} preset creates an editable inference provider atomically`, async ({
    page,
  }, testInfo) => {
    await page.route('http://proxy.test/_proxy/api/state', (route) =>
      route.fulfill({
        json: {
          ...snapshot,
          presets: [{ family: 'inference', id, label, upstream, auth: { header, prefix } }],
          secrets: [{ name: 'provider-key', routes: null, source: 'stored' }],
        },
      }),
    );
    await page.route('http://proxy.test/_proxy/api/providers', (route) =>
      route.fulfill({ json: { revision: 2 } }),
    );
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await choosePreset(page, label);
    await expect(page.getByRole('heading', { name: `Set up ${label}`, exact: true })).toBeVisible();
    const labelField = page.getByLabel('Label', { exact: true });
    await expect(labelField).toHaveValue(label);
    await expect(page.getByLabel('Provider name', { exact: true })).toHaveValue(id);
    await expect(page.getByLabel('Upstream base URL', { exact: true })).toHaveValue(upstream);
    await page.getByLabel('Credential source').selectOption('existing');
    await page.getByLabel('Secret name', { exact: true }).selectOption('provider-key');
    await page.getByLabel('Provider name', { exact: true }).fill(`${id}-mine`);
    const customLabel = `Team ${label} API`;
    await labelField.fill(customLabel);
    await page.screenshot({ path: testInfo.outputPath(`preset-${id}.png`), fullPage: true });
    const saved = page.waitForRequest('http://proxy.test/_proxy/api/providers');
    await page.getByRole('button', { name: 'Create provider', exact: true }).click();
    expect((await saved).postDataJSON()).toEqual({
      revision: 1,
      name: `${id}-mine`,
      preset: id,
      label: customLabel,
      secret: 'provider-key',
    });
    await expect(page.getByRole('status')).toContainText('No upstream request was made');
  });
}

test('OpenCode Go preset is one route with one shared key and three subpath mappings', async ({
  page,
}, testInfo) => {
  const preset = {
    family: 'inference',
    id: 'opencode-go',
    label: 'OpenCode Go',
    // Actual backend preset: the rules own every destination.
    upstream: '',
    auth: { header: '', prefix: '' },
    billing: 'plan',
    usage: 'auto',
    rules: [
      {
        path_prefix: '/',
        upstream: 'https://opencode.ai/zen/go/v1',
        auth: { header: 'Authorization', prefix: 'Bearer ' },
      },
      {
        path_prefix: '/messages/',
        upstream: 'https://opencode.ai/zen/go',
        auth: { header: 'x-api-key', prefix: '' },
      },
      {
        path_prefix: '/gemini/',
        upstream: 'https://opencode.ai/inference/go/v1',
        auth: { header: 'x-goog-api-key', prefix: '' },
      },
    ],
  };
  await page.route('http://proxy.test/_proxy/api/state', (route) =>
    route.fulfill({
      json: {
        ...snapshot,
        presets: [preset],
        secrets: [{ name: 'opencode-go-key', routes: null, source: 'stored' }],
      },
    }),
  );
  await page.route('http://proxy.test/_proxy/api/providers', (route) =>
    route.fulfill({ json: { revision: 2 } }),
  );
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  await choosePreset(page, 'OpenCode Go');
  await expect(page.getByLabel('Provider name', { exact: true })).toHaveValue('opencode-go');
  await expect(page.getByText('/ → https://opencode.ai/zen/go/v1', { exact: true })).toBeVisible();
  await expect(
    page.getByText('/messages/ → https://opencode.ai/zen/go', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('/gemini/ → https://opencode.ai/inference/go/v1', { exact: true }),
  ).toBeVisible();
  await expect(page.locator('input[type=password]')).toHaveCount(1);
  await page.getByLabel('Credential source').selectOption('existing');
  await page.getByLabel('Secret name', { exact: true }).selectOption('opencode-go-key');
  await page.screenshot({ path: testInfo.outputPath('preset-opencode-go.png'), fullPage: true });
  const saved = page.waitForRequest('http://proxy.test/_proxy/api/providers');
  await page.getByRole('button', { name: 'Create provider', exact: true }).click();
  expect((await saved).postDataJSON()).toEqual({
    revision: 1,
    name: 'opencode-go',
    preset: 'opencode-go',
    label: 'OpenCode Go',
    secret: 'opencode-go-key',
  });
  await expect(page.getByRole('status')).toContainText('No upstream request was made');
});

test('adds a missing API key without leaving or resetting the route', async ({
  page,
}, testInfo) => {
  let stored = false;
  let submittedKey: unknown;
  await page.route('http://proxy.test/_proxy/api/state', (route) =>
    route.fulfill({
      json: {
        ...snapshot,
        revision: stored ? 2 : 1,
        secrets: stored ? [{ name: 'my-venice-key', routes: null, source: 'stored' }] : [],
      },
    }),
  );
  await page.route('http://proxy.test/_proxy/api/secrets', (route) => {
    submittedKey = route.request().postDataJSON();
    stored = true;
    return route.fulfill({ json: { revision: 2 } });
  });
  await page.route('http://proxy.test/_proxy/api/config', (route) =>
    route.fulfill({ json: { revision: 3 } }),
  );
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  await choosePreset(page, 'Custom endpoint');
  await page.getByLabel('Name', { exact: true }).fill('my-venice');
  await page.getByLabel('Local prefix', { exact: true }).fill('/my-custom-path/');
  await page.getByLabel('Upstream base URL', { exact: true }).fill('https://api.venice.ai/api/v1');
  await page.getByRole('button', { name: 'Add substitution', exact: true }).click();
  await page.getByLabel('Header name').fill('Authorization');
  await page.getByRole('button', { name: 'Add API key', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: 'Add API key' });
  await expect(dialog.getByLabel(/Bound origin|Use route origin/)).toHaveCount(0);
  await expect(dialog.getByLabel('New secret value', { exact: true })).toBeFocused();
  await expect(page.locator('.route-form form')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog')).toHaveCount(0);
  expect(stored).toBe(false);
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('my-venice');
  await page.getByRole('button', { name: 'Add API key', exact: true }).click();
  dialog = page.getByRole('dialog', { name: 'Add API key' });
  await page.screenshot({ path: testInfo.outputPath('add-api-key-dialog.png'), fullPage: true });
  await dialog
    .getByLabel('New secret value', { exact: true })
    .fill('synthetic-private-provider-key');
  await dialog.getByRole('button', { name: 'Store secret', exact: true }).click();
  await expect(page.locator('dialog')).toHaveCount(0);
  expect(submittedKey).toEqual({
    name: 'my-venice-key',
    value: 'synthetic-private-provider-key',
  });
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('my-venice');
  await expect(page.getByLabel('Local prefix', { exact: true })).toHaveValue('/my-custom-path/');
  await expect(page.getByRole('heading', { name: 'Custom endpoint' })).toBeVisible();
  await expect(page.getByLabel('Secret name', { exact: true })).toHaveValue('my-venice-key');
  await expect(page.getByRole('status')).toContainText('Your route is not saved yet');
  const saved = page.waitForRequest('http://proxy.test/_proxy/api/config');
  await page.getByRole('button', { name: 'Save route', exact: true }).click();
  expect((await saved).postDataJSON()).toEqual({
    revision: 2,
    providers: { 'my-venice': { label: 'my-venice' } },
    defaults: null,
    routes: [expect.objectContaining({ name: 'my-venice', local_prefix: '/my-custom-path/' })],
  });
});

test('Clients labels keep legacy bookmarks and API state compatible', async ({ page }) => {
  await page.goto('http://proxy.test/_proxy/?tab=callers');
  await expect(page.getByRole('button', { name: 'Clients', exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page.getByRole('heading', { name: 'Clients', exact: true })).toBeVisible();
  await expect(page.getByLabel('Client name', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Create client', exact: true })).toBeVisible();
  await expect(page).toHaveURL('http://proxy.test/_proxy/?tab=clients');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Clients', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Callers', exact: true })).toHaveCount(0);
});

test('Clients issues tokens for approved providers and shows copy toasts without shifting layout', async ({
  page,
  hasTouch,
}) => {
  await page.addInitScript(() => {
    const copies: string[] = [];
    Object.defineProperty(window, 'proxyCopies', { value: copies });
    Object.defineProperty(navigator, 'clipboard', {
      value: {
        writeText: async (text: string) => {
          copies.push(text);
        },
      },
    });
  });
  const token = 'provider-client-token';
  const routeDefaults = {
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  };
  const routes = [
    {
      ...routeDefaults,
      provider: 'alpha',
      profile: '',
      serves: [],
      name: 'alpha',
      local_prefix: '/alpha/',
      upstream: 'https://alpha.example.test/v1',
    },
    {
      ...routeDefaults,
      provider: 'alpha-extra',
      profile: '',
      serves: [],
      name: 'alpha-extra',
      local_prefix: '/alpha-extra/',
      upstream: 'https://alpha.example.test/extra',
    },
    {
      ...routeDefaults,
      provider: 'beta',
      profile: '',
      serves: [],
      name: 'beta',
      local_prefix: '/beta/',
      upstream: 'https://beta.example.test/v1',
    },
  ];
  const state: ProxyState = {
    ...snapshot,
    auth: 'token',
    revision: 3,
    routes,
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'alpha',
        label: 'Alpha',
        routes: ['alpha', 'alpha-extra'],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'beta',
        label: 'Beta',
        routes: ['beta'],
      },
    ],
    callers: [],
  };
  const creations: unknown[] = [];
  await page.route('http://proxy.test/_proxy/api/**', async (request) => {
    const url = new URL(request.request().url());
    if (url.pathname.endsWith('/state'))
      return request.fulfill({ json: withSingletonProviders(state) });
    if (url.pathname.endsWith('/callers') && request.request().method() === 'POST') {
      const body = request.request().postDataJSON() as {
        name: string;
        providers: string[];
        revision: number;
      };
      creations.push(body);
      const caller = {
        id: 'browser-worker',
        name: body.name,
        grants: state
          .providers!.filter((provider) => body.providers.includes(provider.id))
          .flatMap((provider) => provider.routes),
        revoked: false,
        created: 1,
        created_by: '',
        external_id: '',
      };
      state.callers = [caller];
      state.revision++;
      return request.fulfill({
        status: 201,
        json: { token, caller },
      });
    }
    return request.fallback();
  });
  await page.goto('http://proxy.test/_proxy/?tab=clients');
  await expect(page.getByRole('heading', { name: 'Clients', exact: true })).toBeVisible();
  const providerFieldset = page
    .locator('.client-composer')
    .getByRole('group', { name: 'Providers' });
  await expect(providerFieldset).toBeVisible();
  // Providers are named by ID; route names and labels stay out of the chips.
  await expect(providerFieldset.getByText('alpha', { exact: true })).toBeVisible();
  await expect(providerFieldset.getByText('beta', { exact: true })).toBeVisible();
  await expect(providerFieldset.getByText('Alpha', { exact: true })).toHaveCount(0);
  await expect(providerFieldset.getByText('alpha-extra', { exact: true })).toHaveCount(0);
  const geometry = await page.evaluate(() => {
    const composer = document.querySelector<HTMLElement>('.client-composer')!;
    const nameField = composer.querySelector<HTMLElement>('.field')!.getBoundingClientRect();
    const chips = composer.querySelector<HTMLElement>('.grant-chips')!.getBoundingClientRect();
    const note = composer
      .querySelector<HTMLElement>('.client-composer-note')!
      .getBoundingClientRect();
    const footer = composer
      .querySelector<HTMLElement>('.client-composer-actions')!
      .getBoundingClientRect();
    return {
      composerRight: composer.getBoundingClientRect().right,
      nameBottom: nameField.bottom,
      chipsTop: chips.top,
      chipsBottom: chips.bottom,
      chipsRight: chips.right,
      noteTop: note.top,
      noteBottom: note.bottom,
      footerTop: footer.top,
    };
  });
  expect(geometry.nameBottom).toBeLessThanOrEqual(geometry.chipsTop);
  expect(geometry.chipsBottom).toBeLessThanOrEqual(geometry.noteTop);
  expect(geometry.noteBottom).toBeLessThanOrEqual(geometry.footerTop);
  expect(geometry.chipsRight).toBeLessThanOrEqual(geometry.composerRight);
  await page.getByLabel('Client name', { exact: true }).fill('Browser worker');
  await page.locator('[data-provider="alpha"]').check();
  await page.getByRole('button', { name: 'Create client', exact: true }).click();
  await expect(page.getByLabel('Client token', { exact: true })).toHaveValue(token);
  await expect(
    page.getByRole('heading', { name: 'term-llm client configuration', exact: true }),
  ).toBeVisible();
  const yamlPrefix = 'proxy:\n  url: http://proxy.test\n  token: ';
  const safeYAML = `${yamlPrefix}file:///run/secrets/proxy-client-token`;
  await expect(page.locator('.config-snippet pre code')).toHaveText(safeYAML);

  // Compare document coordinates so Playwright scrolling a copy button into view
  // does not look like a layout shift.
  const copyLayout = () =>
    page.evaluate(() =>
      Array.from(
        document.querySelectorAll(
          '.page-intro, .client-token-panel, .token-copy-row input, .token-copy-row button, .config-snippet-head button, .config-snippet pre, .client-token-footer',
        ),
        (node) => {
          const { x, y, width, height } = node.getBoundingClientRect();
          return { x: x + window.scrollX, y: y + window.scrollY, width, height };
        },
      ),
    );
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const touchTarget = await page.evaluate(
    () => matchMedia('(pointer: coarse), (width <= 40rem)').matches,
  );
  const region = page.locator('.toast-region');
  const notice = region.getByRole('status');
  const copyCases = [
    { label: 'Copy token', message: /^Client token copied\b/, payload: token },
    { label: 'Copy YAML', message: /^YAML copied\b/, payload: safeYAML },
    {
      label: 'Copy with token',
      message: /^YAML with token copied\b/,
      payload: `${yamlPrefix}'${token}'`,
    },
  ];
  const expectedCopies: string[] = [];
  for (const { label, message, payload } of copyCases) {
    const copy = page.getByRole('button', { name: label, exact: true });
    await expect(copy).toHaveText(label);
    const before = await copyLayout();
    await copy.click();
    await expect(notice).toBeVisible();
    // Match the start of the message without assuming where the icon or close button sits.
    await expect(notice.getByText(message)).toBeVisible();
    expectedCopies.push(payload);
    expect(
      await page.evaluate(() => (window as unknown as { proxyCopies: string[] }).proxyCopies),
    ).toEqual(expectedCopies);
    // Check visible text too: an unchanged aria-label can hide a text swap.
    expect(await copy.innerText()).toBe(label);
    expect(await copyLayout()).toEqual(before);

    await expect(region).toHaveCSS('position', 'fixed');
    const { top, rightGap } = await region.evaluate((node) => {
      const { top, right } = node.getBoundingClientRect();
      return { top, rightGap: document.documentElement.clientWidth - right };
    });
    expect(top).toBeGreaterThanOrEqual(0);
    expect(top).toBeLessThan(48);
    expect(rightGap).toBeGreaterThanOrEqual(0);
    expect(rightGap).toBeLessThan(48);

    const dismiss = notice.getByRole('button', { name: 'Dismiss notification', exact: true });
    if (touchTarget && label === 'Copy token') {
      // Hit-test just inside all four corners of a 44px square, then dismiss off-center.
      const { x, y, cornersHit } = await dismiss.evaluate((button) => {
        const rect = button.getBoundingClientRect();
        const centerX = rect.x + rect.width / 2;
        const centerY = rect.y + rect.height / 2;
        return {
          x: centerX + 21,
          y: centerY + 21,
          cornersHit: [-21, 21].every((dx) =>
            [-21, 21].every(
              (dy) => document.elementFromPoint(centerX + dx, centerY + dy) === button,
            ),
          ),
        };
      });
      expect(cornersHit).toBe(true);
      if (hasTouch) await page.touchscreen.tap(x, y);
      else await page.mouse.click(x, y);
    } else {
      await dismiss.click();
    }
    await expect(notice).toBeHidden();
    expect(await copyLayout()).toEqual(before);
  }

  expect(await page.locator('#proxy-app').textContent()).not.toContain(token);
  expect(creations).toEqual([{ name: 'Browser worker', providers: ['alpha'], revision: 3 }]);
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage })),
  ).not.toContain(token);
});

test('Clients fits the longest alphabetic provider summary as the row resizes', async ({
  page,
}) => {
  // The long third name forces truncation on mobile; the short last name
  // would fit, but must not be picked out of alphabetic order.
  const longName = 'Gamma-Worldwide-Managed-Model-Workspace-West';
  const labels = ['Zulu', longName, 'Beta', 'alpha'];
  const routes = labels.map((_, index) => ({
    name: `route-${index}`,
    provider: labels[index],
    enabled: true,
    profile: '',
    serves: [],
    local_prefix: `/route-${index}/`,
    upstream: 'https://example.test/v1',
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  }));
  const state: ProxyState = {
    ...snapshot,
    routes,
    providers: routes.map((route, index) => ({
      id: route.provider,
      label: labels[index],
      family: 'inference',
      status: 'green',
      reason: '',
      routes: [route.name],
    })),
    callers: [
      { id: 'all', name: 'All approvals', grants: routes.map((route) => route.name) },
      { id: 'pair', name: 'Two approvals', grants: [routes[0].name, routes[1].name] },
    ].map((caller) => ({ ...caller, created: 1, created_by: '', external_id: '', revoked: false })),
  };
  await page.route('http://proxy.test/_proxy/api/state', (route) => route.fulfill({ json: state }));
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('http://proxy.test/_proxy/?tab=clients');
  const row = page.locator('[data-focus="caller:all"]');
  const all = row.locator('.client-grants');
  const pair = page.locator('[data-focus="caller:pair"] .client-grants');
  const full = `alpha, Beta, ${longName}, Zulu`;
  await expect(all).toHaveText(full, { useInnerText: true });
  await expect(pair).toHaveText(`${longName}, Zulu`, { useInnerText: true });

  await page.setViewportSize({ width: 320, height: 900 });
  await expect(all).toHaveText('alpha, Beta and 2 more', { useInnerText: true });
  await expect(pair).toHaveText('2 providers', { useInnerText: true });
  await row.locator('.client-toggle').click();
  const approvals = row.getByRole('group', { name: 'Provider approvals' });
  for (const name of labels) {
    await expect(approvals.getByRole('checkbox', { name, exact: true })).toBeChecked();
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(all).toHaveText(full, { useInnerText: true });
  await expect(pair).toHaveText(`${longName}, Zulu`, { useInnerText: true });
});

test('Clients preserves legacy partial approvals and edits only explicit providers', async ({
  page,
}) => {
  const routeDefaults = {
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  };
  const state: ProxyState = {
    ...snapshot,
    auth: 'token',
    revision: 5,
    routes: [
      {
        ...routeDefaults,
        provider: 'alpha',
        profile: '',
        serves: [],
        name: 'alpha',
        local_prefix: '/alpha/',
        upstream: 'https://alpha.example.test/v1',
      },
      {
        ...routeDefaults,
        provider: 'alpha-extra',
        profile: '',
        serves: [],
        name: 'alpha-extra',
        local_prefix: '/alpha-extra/',
        upstream: 'https://alpha.example.test/extra',
      },
      {
        ...routeDefaults,
        provider: 'beta',
        profile: '',
        serves: [],
        name: 'beta',
        local_prefix: '/beta/',
        upstream: 'https://beta.example.test/v1',
      },
    ],
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'alpha',
        label: 'Alpha',
        routes: ['alpha', 'alpha-extra'],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'beta',
        label: 'Beta',
        routes: ['beta'],
      },
    ],
    callers: [
      {
        id: 'legacy',
        name: 'Legacy worker',
        grants: ['alpha'],
        revoked: false,
        created: 1,
        created_by: '',
        external_id: '',
      },
    ],
  };
  const changes: unknown[] = [];
  await page.route('http://proxy.test/_proxy/api/**', async (request) => {
    const url = new URL(request.request().url());
    if (url.pathname.endsWith('/state'))
      return request.fulfill({ json: withSingletonProviders(state) });
    if (url.pathname.endsWith('/callers') && request.request().method() === 'PUT') {
      const body = request.request().postDataJSON() as {
        id: string;
        provider_changes: { id: string; approved: boolean }[];
        revision: number;
      };
      changes.push(body);
      expect(body).not.toHaveProperty('grants');
      const caller = state.callers.find((value) => value.id === body.id)!;
      for (const change of body.provider_changes) {
        const provider = state.providers!.find((value) => value.id === change.id)!;
        caller.grants = change.approved
          ? [...new Set([...caller.grants, ...provider.routes])]
          : caller.grants.filter((route) => !provider.routes.includes(route));
      }
      state.revision++;
      return request.fulfill({ json: { ok: true } });
    }
    return request.fallback();
  });
  await page.goto('http://proxy.test/_proxy/?tab=clients');
  const row = page.locator('[data-focus="caller:legacy"]');
  await expect(row.locator('.client-grants')).toContainText('alpha (needs approval)');
  await expect(row).not.toContainText('alpha-extra');
  await row.locator('.client-toggle').click();
  const editor = row.locator('.client-detail form');
  await expect(editor.getByRole('group', { name: 'Provider approvals' })).toBeVisible();
  await expect(editor.getByText('alpha-extra', { exact: true })).toHaveCount(0);
  const alpha = editor.locator('[data-provider="alpha"]');
  await expect(alpha).not.toBeChecked();
  expect(await alpha.evaluate((node) => (node as HTMLInputElement).indeterminate)).toBe(true);
  await alpha.check();
  await editor.getByRole('button', { name: 'Save approvals', exact: true }).click();
  expect(changes).toEqual([
    {
      id: 'legacy',
      provider_changes: [{ id: 'alpha', approved: true }],
      revision: 5,
    },
  ]);
  await expect(row.locator('.client-grants')).toContainText('alpha');
  await expect(row.locator('.client-grants')).not.toContainText('needs approval');
});

test('Providers labels keep legacy route bookmarks and API state compatible', async ({ page }) => {
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  await expect(page.getByRole('button', { name: 'Providers', exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add provider', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Routes', exact: true })).toHaveCount(0);
  await expect(page).toHaveURL('http://proxy.test/_proxy/?tab=routes');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add provider', exact: true })).toBeVisible();
});

for (const width of [1440, 320]) {
  test(`request Filters matches Refresh and toggles natively at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.route('http://proxy.test/_proxy/api/requests?*', (route) =>
      route.fulfill({ json: { total: 0, page: 1, page_size: 25, requests: [] } }),
    );
    // The ledger re-fetches page one every second with the applied filters.
    // Freeze the page clock before the first poll so every list request below
    // is attributable to the control under test.
    await page.clock.install();
    const initialLoad = waitForLedgerRequest(page, (params) => params.get('page') === '1');
    await page.goto('http://proxy.test/_proxy/?tab=requests');
    await page.clock.pauseAt(Date.now() + 250);
    const disclosure = page.locator('.request-filters');
    const summary = disclosure.locator('summary');
    const refresh = page.getByRole('button', { name: 'Refresh', exact: true });
    await expect(page.getByRole('button', { name: 'Refresh state', exact: true })).toHaveCount(0);
    await expect(summary).toHaveText('Filters');
    await expect(refresh).toBeEnabled();
    expectQuery(await initialLoad, { page: '1', page_size: '25' });
    await expect(page.getByLabel('Request results', { exact: true })).toContainText(
      'No matching requests',
    );
    const controlStyle = (element: Element) => {
      const style = getComputedStyle(element);
      return [
        style.fontFamily,
        style.fontSize,
        style.fontWeight,
        style.lineHeight,
        style.letterSpacing,
        style.color,
        style.backgroundColor,
        style.borderRadius,
      ];
    };
    expect(await summary.evaluate(controlStyle)).toEqual(await refresh.evaluate(controlStyle));
    const bounds = (await summary.boundingBox())!;
    const refreshBounds = (await refresh.boundingBox())!;
    expect(bounds.y).toBe(refreshBounds.y);
    expect(bounds.height).toBe(refreshBounds.height);
    expect(bounds.x + bounds.width).toBeLessThan(refreshBounds.x);
    if (width === 320) {
      expect(bounds.height).toBeGreaterThanOrEqual(44);
      expect(bounds.width).toBeGreaterThanOrEqual(44);
    }
    const alignment = await summary.evaluate((element) => {
      const range = document.createRange();
      range.selectNodeContents(element);
      const label = range.getBoundingClientRect();
      const trigger = element.getBoundingClientRect();
      const marker = getComputedStyle(element, '::after');
      const gap = parseFloat(getComputedStyle(element).columnGap);
      return {
        x:
          label.left +
          (label.width + gap + parseFloat(marker.width)) / 2 -
          trigger.x -
          trigger.width / 2,
        y: label.top + label.height / 2 - trigger.y - trigger.height / 2,
        marker: marker.content,
      };
    });
    expect(alignment.marker).toContain('›');
    expect(Math.abs(alignment.x)).toBeLessThan(1);
    expect(Math.abs(alignment.y)).toBeLessThan(1);

    const path = page.getByLabel('Path', { exact: true });
    await expect(path).toBeHidden();
    await refresh.focus();
    await page.keyboard.press('Shift+Tab');
    await expect(summary).toBeFocused();
    await expect(summary).toHaveCSS('outline-style', 'solid');
    await page.keyboard.press('Enter');
    await expect(disclosure).toHaveAttribute('open');
    await expect(path).toBeVisible();
    const clearFilters = page.getByRole('button', { name: 'Clear', exact: true });
    if (width === 320) {
      for (const control of [page.getByRole('button', { name: 'Apply filters' }), clearFilters]) {
        const bounds = (await control.boundingBox())!;
        expect(bounds.height).toBeGreaterThanOrEqual(44);
        expect(bounds.x).toBeGreaterThanOrEqual(0);
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
      }
    }
    const rotation = () =>
      summary.evaluate((element) => getComputedStyle(element, '::after').rotate);
    expect(await rotation()).toBe('90deg');
    await path.fill('/v1/messages');
    await expect(summary).toHaveText('Filters');
    const apply = waitForLedgerRequest(page, (params) => params.get('path') === '/v1/messages');
    await path.press('Enter');
    expectQuery(await apply, { path: '/v1/messages', page: '1', page_size: '25' });
    await expect(summary).toHaveText('Filters (1)');
    await path.fill('/draft');
    await summary.focus();
    await page.keyboard.press('Space');
    await expect(disclosure).not.toHaveAttribute('open');
    await expect(summary).toBeFocused();
    await expect(summary).toHaveText('Filters (1)');
    expect(await rotation()).toBe('none');

    const reload = waitForLedgerRequest(
      page,
      (params) => params.get('path') === '/v1/messages' && params.get('page') === '1',
    );
    await refresh.click();
    expectQuery(await reload, { path: '/v1/messages', page: '1', page_size: '25' });
    await expect(disclosure).not.toHaveAttribute('open');
    await expect(refresh).toBeEnabled();
    await summary.press('Enter');
    await expect(path).toHaveValue('/draft');
    const clear = waitForLedgerRequest(
      page,
      (params) => params.get('page') === '1' && !params.has('path'),
    );
    await clearFilters.click();
    expectQuery(await clear, { page: '1', page_size: '25' });
    await expect(summary).toHaveText('Filters');
    await expect(path).toHaveValue('');
  });
}

for (const width of [1440, 1024, 833, 832, 641, 640, 390, 320]) {
  test(`request ledger aligns metrics and expands grouped failures at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const started = Date.now();
    await page.route('http://proxy.test/_proxy/api/debug/captures/request-*', (route) =>
      route.fulfill({ status: 404, json: { error: 'Capture not found' } }),
    );
    await page.route('http://proxy.test/_proxy/api/requests?*', (route) => {
      const number = Number(new URL(route.request().url()).searchParams.get('page'));
      return route.fulfill({
        json: {
          total: 2225,
          page: number,
          page_size: 25,
          next: number < 89 ? 2225 - number * 25 : 0,
          requests: Array.from({ length: 25 }, (_, i) => {
            const failed = i >= 3 && i <= 5;
            const openai = i === 2;
            const usage = failed
              ? undefined
              : {
                  protocol: openai ? 'responses' : 'messages',
                  usage: {
                    source: 'reported',
                    model: openai ? 'gpt-5.5' : 'claude-haiku-5-5',
                    input: openai ? 1000 : i === 0 ? 2 : i === 1 ? 14 : 0,
                    output: openai ? 7000 : i === 0 ? 19 : i === 1 ? 846 : 0,
                    cached_input: openai ? 750 : i === 1 ? 36098 : 0,
                    cache_write: openai ? -1 : i === 0 ? 36098 : 0,
                    reasoning: openai ? 5100 : -1,
                    total: -1,
                    turns: 1,
                    tier: 'standard',
                    ambiguous: false,
                  },
                };
            return {
              id: `request-${number}-${i}`,
              seq: 2225 - (number - 1) * 25 - i,
              started: started - (i === 24 ? 86400000 : i * 1000),
              ended: started,
              caller_id: 'arch',
              caller_name: 'arch',
              route: failed ? 'venice' : openai ? 'openai' : 'claude',
              revision: 17,
              method: 'POST',
              local_prefix: failed ? '/venice/' : openai ? '/openai/' : '/claude/',
              path: failed ? '/chat/completions' : '/v1/messages',
              peer: '127.0.0.1',
              status: failed ? 400 : 200,
              outcome: failed ? 'http_error' : openai ? 'incomplete' : 'complete',
              sent: 113112,
              received: 5059,
              total_ms: i === 0 ? 1916 : 230,
              header_ms: 200,
              first_byte_ms: 210,
              upstream_id: '',
              usage,
              request_meta: failed
                ? undefined
                : {
                    state: 'observed',
                    model: 'requested-model',
                    reasoning_effort: 'high',
                    output_limit: 20000,
                    temperature: 0,
                    top_p: 0,
                    stream: false,
                    tool_choice: 'auto',
                    parallel_tools: false,
                    declared_tools: 0,
                    reasoning_mode: 'pro',
                    reasoning_context: 'current_turn',
                    verbosity: 'low',
                    multi_agent: false,
                    agent_limit: 3,
                    cache_mode: 'explicit',
                    cache_ttl: '30m',
                  },
              response_meta: failed
                ? undefined
                : { state: 'observed', finish: openai ? 'length' : 'end_turn' },
              money: failed
                ? { billing: 'pay_as_you_go', kind: 'unmeasured' }
                : {
                    billing: 'plan',
                    kind: 'included',
                    api_equivalent: {
                      kind: 'estimated',
                      amount_micros: i === 0 ? 4522 : i === 1 ? 786 : 0,
                      currency: 'USD',
                      source: 'models.dev/anthropic',
                      asof: '2026-11-17',
                    },
                  },
            };
          }),
        },
      });
    });
    await page.goto('http://proxy.test/_proxy/?tab=requests');
    const top = page.getByRole('navigation', { name: 'Request pages', exact: true });
    const pager = page.getByRole('navigation', { name: 'Request pages (bottom)', exact: true });
    await expect(top).toContainText('Page 1 of 89');
    await expect(page.locator('.request-row')).toHaveCount(23);
    await expect(page.locator('.request-filters')).not.toHaveAttribute('open');
    const first = page.locator('.request-row').first();
    await expect(first.locator('.request-input')).toHaveText('36.1k');
    await expect(first.locator('.request-price')).toContainText('0.0045');
    await expect(first.locator('.request-price')).toContainText('API');
    await expect(first.locator('.request-time')).toHaveText(/^[0-2]\d:[0-5]\d:[0-5]\d$/);
    await expect(first.locator('.request-duration')).toHaveText('1.9s');
    await expect(first.locator('.request-open')).toHaveAccessibleDescription('claude-haiku-5-5');
    await expect(page.locator('.request-row').nth(2).locator('.request-input')).toHaveText('1k');
    await expect(page.locator('.request-row').nth(2).locator('.request-cache')).toHaveText('75%');
    await expect(page.locator('.request-row').nth(2).locator('.request-status')).toHaveClass(
      /status-warning/,
    );
    await expect(first.locator('.request-status')).not.toHaveClass(/outcome/);
    await expect(page.locator('.request-date-group')).toHaveCount(2);
    const wide = width > 1024;
    if (wide) {
      // Layout follows width; touch devices retain larger hit targets.
      expect((await first.boundingBox())!.height).toBeLessThanOrEqual(60);
      for (const name of ['input', 'output', 'cache', 'duration', 'price']) {
        const edges = await page
          .locator(`.request-row > .request-${name}`)
          .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().right));
        expect(Math.max(...edges) - Math.min(...edges)).toBeLessThan(1);
      }
    } else {
      expect((await first.boundingBox())!.height).toBeLessThanOrEqual(width <= 320 ? 96 : 76);
      if (width <= 640) {
        await expect(first.locator('.request-mobile-usage')).toBeVisible();
        await expect(first.locator('.request-input')).not.toBeVisible();
      } else {
        await expect(first.locator('.request-input')).toBeVisible();
        await expect(first.locator('.request-cache')).toBeVisible({ visible: width > 832 });
      }
    }
    const fallback = page.locator('.request-row').nth(3);
    await expect(fallback.locator('.request-open')).toHaveText('POST /venice/chat/completions');
    if (width > 640) {
      await expect(fallback.locator('.request-input')).toHaveText('~113.11K');
      await expect(fallback.locator('.request-output')).toHaveText('~5.06K');
    } else {
      await expect(fallback.locator('.request-mobile-usage')).toHaveText('~113.11K→~5.06K');
    }
    const repeated = page.getByRole('button', { name: 'Expand 3 repeated requests' });
    await repeated.click();
    await expect(page.locator('.request-group-rows .request-row')).toHaveCount(3);
    await page.getByRole('button', { name: 'Collapse 3 repeated requests' }).click();
    await expect(page.locator('.request-group-rows .request-row')).toHaveCount(0);
    await first.locator('.request-open').click();
    await expect(first.locator('.request-detail')).toBeVisible();
    await expect(first.locator('.request-detail')).toContainText('Cache write');
    await expect(first.locator('.request-detail')).toContainText('No raw payload capture');
    const detail = first.locator('.request-detail');
    await expect(detail.getByRole('heading', { name: 'Requested', exact: true })).toBeVisible();
    await expect(detail.getByRole('heading', { name: 'Response', exact: true })).toBeVisible();
    await expect(detail).toContainText('RequestedModelrequested-model');
    await expect(detail).toContainText('Reasoning scope');
    await expect(detail).toContainText('Agent limit');
    await expect(detail.locator('details')).toHaveCount(0);
    const requestedValue = detail
      .locator('.request-insights-grid > div')
      .first()
      .locator('dd')
      .first();
    const metadataValue = detail.locator('.request-meta dd').first();
    expect(
      Math.abs((await requestedValue.boundingBox())!.x - (await metadataValue.boundingBox())!.x),
    ).toBeLessThan(1);
    const thought = page
      .locator('.request-row')
      .nth(2)
      .locator(width <= 640 ? '.request-compact-thinking' : '.request-thinking');
    await expect(thought).toHaveText('5.1k think');
    await expect(first.locator('.request-thinking')).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: testInfo.outputPath(`insights-${width}.png`), fullPage: true });

    await expect(first.getByRole('heading', { name: 'Request details' })).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(first.locator('.request-detail')).toHaveCount(0);
    await expect(first.locator('.request-open')).toBeFocused();
    await pager.getByRole('button', { name: 'Page 89', exact: true }).click();
    await expect(pager).toContainText('Page 89 of 89');
    await expect(pager.getByRole('button', { name: 'Next' })).toBeDisabled();
    await pager.getByRole('button', { name: 'Page 1', exact: true }).click();
    await expect(top).toContainText('Page 1 of 89');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: testInfo.outputPath(`ledger-${width}.png`), fullPage: true });
    await page.getByText('Filters', { exact: true }).click();
    await expect(page.getByLabel('Path', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  });
}

test('proxy Debug toggles capture and safely browses bounded payloads on mobile', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  let enabled = false;
  let changes = 0;
  const payload = '<script>window.debugInjected=true</script>' + 'x'.repeat(70_000) + 'PAYLOAD END';
  await page.route('http://proxy.test/_proxy/api/debug**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === '/_proxy/api/debug') {
      if (request.method() === 'POST') {
        const body = request.postDataJSON();
        if (body.enabled) expect(body.acknowledge_sensitive).toBe(true);
        enabled = body.enabled;
        changes++;
      }
      return route.fulfill({
        json: {
          enabled,
          directory: '/synthetic/debug-payloads',
          preview_limit_bytes: 65_536,
          remaining_bytes: 256 << 20,
          record_count: 1,
          record_limit: 1000,
        },
      });
    }
    if (url.pathname === '/_proxy/api/debug/captures')
      return route.fulfill({
        json: {
          captures: [
            {
              id: 'px_fixture',
              started: 1,
              method: 'POST',
              route: 'fixture',
              local_path: '/fixture/messages',
              outcome: 'complete',
              finished: true,
              websocket: false,
              status: 200,
              files: ['manifest.json', 'response.body'],
            },
          ],
          limit: 50,
        },
      });
    const file = url.pathname.split('/').at(-1);
    const body = file === 'manifest.json' ? '{"id":"px_fixture"}' : payload;
    const offset = Number(url.searchParams.get('offset') || 0);
    const content = body.slice(offset, offset + 65_536);
    return route.fulfill({
      json: {
        id: 'px_fixture',
        file,
        encoding: 'utf8',
        content,
        offset,
        total_bytes: body.length,
        bytes_returned: content.length,
        truncated: offset > 0 || offset + content.length < body.length,
      },
    });
  });
  await page.goto('http://proxy.test/_proxy/?tab=debug');
  await expect(page.getByRole('heading', { name: 'Debug', exact: true })).toBeVisible();
  const enable = page.getByRole('button', { name: 'Enable capture', exact: true });
  await expect(enable).toBeEnabled();
  page.once('dialog', (dialog) => dialog.dismiss());
  await enable.click();
  await expect(enable).toBeEnabled();
  expect(changes).toBe(0);
  page.once('dialog', (dialog) => dialog.accept());
  await enable.click();
  const disable = page.getByRole('button', { name: 'Disable capture', exact: true });
  await expect(disable).toBeEnabled();
  expect(changes).toBe(1);
  await page.getByLabel('Capture', { exact: true }).selectOption('px_fixture');
  await page.getByLabel('File', { exact: true }).selectOption('response.body');
  const preview = page.getByLabel('Capture preview', { exact: true });
  await expect(preview).toContainText('<script>window.debugInjected=true</script>');
  expect(await page.evaluate(() => 'debugInjected' in window)).toBe(false);
  await page.getByRole('button', { name: 'End', exact: true }).click();
  await expect(preview).toContainText('PAYLOAD END');
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true);
  await disable.click();
  await expect(enable).toBeEnabled();
  expect(changes).toBe(2);
  await page.getByRole('button', { name: 'Providers', exact: true }).click();
  await expect(preview).toHaveCount(0);
});

test('Clients stays scoped to generic tokens without a Claude pairing wizard', async ({ page }) => {
  await page.goto('http://proxy.test/_proxy/?tab=secrets');
  await expect(page.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
  await expect(page.locator('header')).not.toContainText('Connect Claude');
  await expect(page.getByRole('button', { name: 'Connect Claude', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Clients', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Clients', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Create client', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Connect Claude', exact: true })).toHaveCount(0);
  await expect(page.locator('header')).not.toContainText('Connect Claude');
  await page.getByRole('button', { name: 'Providers', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Connect Claude', exact: true })).toHaveCount(0);
});

for (const width of [1440, 390]) {
  test(`Provider list keeps one compact status per provider at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    let enabled = true;
    await page.route('http://proxy.test/_proxy/api/state', (route) => {
      const defaults = {
        enabled,
        allow_insecure_http: false,
        caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
        set_headers: {},
      };
      const state: ProxyState = {
        ...snapshot,
        accounts: [
          {
            name: 'native',
            kind: 'claude-bin',
            auth_mode: 'shared_vault',
            state: 'needs_login',
            generation: 1,
            updated: 1,
          },
        ],
        providers: [
          {
            family: 'inference',
            status: 'red',
            reason: 'Sign-in needed',
            id: 'native',
            label: 'Claude Code',
            routes: ['native'],
          },
          {
            family: 'inference',
            status: enabled ? 'green' : 'grey',
            reason: enabled ? '' : 'Disabled',
            id: 'venice',
            label: 'Venice',
            routes: ['venice'],
          },
        ],
        routes: [
          {
            ...defaults,
            provider: 'native',
            profile: '',
            serves: [],
            name: 'native',
            account: 'native',
            local_prefix: '/claude-bin/',
            upstream: 'https://api.anthropic.com',
          },
          {
            ...defaults,
            provider: 'venice',
            profile: 'venice',
            serves: [],
            name: 'venice',
            local_prefix: '/venice/',
            upstream: 'https://api.venice.ai/api/v1',
          },
        ],
      };
      return route.fulfill({ json: withSingletonProviders(state) });
    });
    for (const disabled of [false, true]) {
      enabled = !disabled;
      await page.goto('http://proxy.test/_proxy/?tab=routes');
      const intro = page.locator('.providers-intro');
      await expect(intro.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
      await expect(intro.locator('p')).toHaveText('2 providers · 1 needs attention');
      await expect(
        intro.locator('.providers-intro-actions').getByRole('button', { name: 'Add provider' }),
      ).toBeVisible();
      const list = page.getByRole('navigation', { name: 'Providers', exact: true });
      await expect(list.getByRole('heading', { name: 'Inference', exact: true })).toBeVisible();
      await expect(list.getByLabel('Filter providers', { exact: true })).toHaveCount(0);
      await expect(list.locator('.provider-list-name')).toHaveText(['native', 'venice']);
      const claude = providerItem(page, 'native');
      const venice = providerItem(page, 'venice');
      await expect(claude).toHaveAccessibleName(
        'native (Claude Code), Sign-in needed, 0 clients with access',
      );
      await expect(venice).toHaveAccessibleName(
        `venice (Venice), ${disabled ? 'Disabled, ' : ''}0 clients with access`,
      );
      await expect(claude.locator('.provider-list-note')).toHaveText('Sign-in needed');
      await expect(venice.locator('.provider-list-note')).toHaveText(
        disabled ? 'Disabled' : 'Venice',
      );
      for (const [item, status] of [
        [claude, 'red'],
        [venice, disabled ? 'grey' : 'green'],
      ] as const) {
        const dot = item.locator('.provider-dot');
        await expect(dot).toHaveCount(1);
        await expect(dot).toBeVisible();
        await expect(dot).toHaveClass(`provider-dot provider-dot-${status}`);
        await expect(item.locator('.provider-list-count')).toHaveText('0');
      }

      await expect(claude).toHaveAttribute('aria-current', 'true');
      const selected = page.locator('#provider-detail');
      await expect(selected).toHaveAttribute('data-provider', 'native');
      await expect(providerTab(selected, 'access')).toHaveAttribute('aria-selected', 'true');
      await expect(selected.locator('.provider-status-red')).toHaveText('Sign-in needed');
      await expect(selected.getByRole('tab')).toHaveCount(4);
      await expect(selected.getByRole('tabpanel')).toHaveCount(1);
      for (const [routeName, label] of [
        ['native', 'Claude Code'],
        ['venice', 'Venice'],
      ]) {
        const detail = await openProvider(page, routeName);
        await expect(page.locator('.provider-detail')).toHaveCount(1);
        await expect(list.locator('.provider-list-item[aria-current="true"]')).toHaveCount(1);
        await expect(detail.getByRole('heading', { name: routeName, exact: true })).toBeVisible();
        await expect(detail).toHaveAccessibleName(`${routeName} (${label})`);
        await expect(detail.locator('.route-access-empty')).toHaveText('No clients approved yet.');
        await expect(
          detail.getByRole('heading', { name: 'Clients with access', exact: true }),
        ).toBeVisible();
        const remove = detail.getByRole('button', {
          name: `Delete provider ${routeName}`,
          exact: true,
        });
        await expect(remove).toBeHidden();
        await providerTab(detail, 'connection').click();
        // Route facts stack into one column on narrow screens.
        const columns = await detail
          .locator('dl.route-facts')
          .first()
          .evaluate((node) => getComputedStyle(node).gridTemplateColumns.split(' ').length);
        expect(columns).toBe(width <= 640 ? 1 : 2);
        await providerTab(detail, 'settings').click();
        await expect(remove).toBeVisible();
      }
      const veniceStatus = selected.locator('.provider-status');
      if (disabled) await expect(veniceStatus).toHaveText('Disabled');
      else await expect(veniceStatus).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
  });

  for (const launcherName of ['claude', 'agy'] as const) {
    const agy = launcherName === 'agy';
    const kind = agy ? 'agy-bin' : 'claude-bin';
    const provider = agy ? 'Antigravity' : 'Claude Code';
    const shortName = agy ? 'Antigravity' : 'Claude';
    const upstream = agy
      ? 'https://daily-cloudcode-pa.googleapis.com'
      : 'https://api.anthropic.com';
    const prefix = `/${kind}/`;
    test(`${provider} shared setup, one-time operator and worker launchers, and delete at ${width}px`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.addInitScript(() => {
        const copies: string[] = [];
        Object.defineProperty(window, 'proxyCopies', { value: copies });
        Object.defineProperty(navigator, 'clipboard', {
          value: {
            writeText: async (text: string) => {
              copies.push(text);
            },
          },
        });
      });
      const state: ProxyState = {
        ...snapshot,
        auth: 'token',
        accounts: [],
        callers: [],
        presets: [
          {
            family: 'inference',
            id: kind,
            label: provider,
            upstream,
            auth: { header: 'Authorization', prefix: 'Bearer ' },
            account_kind: kind,
          },
          {
            family: 'inference',
            id: 'openai',
            label: 'OpenAI',
            upstream: 'https://api.openai.com/v1',
            auth: { header: 'Authorization', prefix: 'Bearer ' },
          },
        ],
      };
      const mutations: { method: string; body: unknown }[] = [];
      const legacyCalls: string[] = [];
      const url = `https://proxy.example.test${prefix}`;
      const operatorToken = 'scoped-browser-canary-operator';
      const workerToken = 'scoped-browser-canary-worker';
      await page.route('http://proxy.test/_proxy/api/**', async (request) => {
        const path = new URL(request.request().url()).pathname;
        if (path.endsWith('/state'))
          return request.fulfill({ json: withSingletonProviders(state) });
        if (
          path === `/_proxy/api/${launcherName}/setup` ||
          path === `/_proxy/api/providers/${kind}`
        ) {
          const method = request.request().method();
          const body = request.request().postDataJSON() as { shared_action?: string };
          mutations.push({ method, body });
          state.revision++;
          if (method === 'DELETE') {
            state.routes = [];
            state.providers = [];
            state.callers.forEach((caller) => {
              caller.grants = [];
            });
            return request.fulfill({ status: 204 });
          }
          const operator = body.shared_action === 'create';
          if (operator) {
            state.accounts = [
              {
                name: 'native',
                kind,
                auth_mode: 'shared_vault',
                state: 'needs_login',
                generation: 1,
                updated: 1,
              },
            ];
            state.routes = [
              {
                serves: [],
                profile: kind,
                provider: kind,
                name: kind,
                account: 'native',
                local_prefix: prefix,
                upstream,
                enabled: true,
                allow_insecure_http: false,
                caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
                set_headers: {},
              },
            ];
            state.providers = [
              {
                family: 'inference',
                status: 'red',
                reason: 'Sign-in needed',
                id: kind,
                label: provider,
                routes: [kind],
              },
            ];
          }
          const caller = {
            id: operator ? 'operator' : 'worker',
            name: operator ? 'laptop' : `${shortName} worker`,
            grants: [kind],
            revoked: false,
            created: 1,
            created_by: '',
            external_id: '',
          };
          state.callers.push(caller);
          return request.fulfill({
            status: 201,
            json: {
              revision: state.revision,
              url,
              token: operator ? operatorToken : workerToken,
              caller_id: caller.id,
            },
          });
        }
        if (path.includes('/connect/') || path.includes('/accounts')) legacyCalls.push(path);
        return request.fallback();
      });
      await page.goto('http://proxy.test/_proxy/?tab=routes');
      await choosePreset(page, provider);
      const heading = page.getByRole('heading', { name: `Set up ${provider}`, exact: true });
      await expect(heading).toBeVisible();
      const setup = page.locator('section').filter({ has: heading });
      const setupActions = setup.getByRole('button').filter({ hasNotText: /^Cancel$/ });
      await expect(setupActions).toHaveText(['Create shared route & sign in']);
      await expect(
        setup.getByRole('button', {
          name: 'Create route & get launcher',
          exact: true,
          includeHidden: true,
        }),
      ).toHaveCount(0);
      await setupActions.click();
      // No client exists yet, so sign-in names a new one instead of minting a launcher.
      await expect(setup.getByLabel('Sign in from client')).toHaveValue('new');
      await setup.getByLabel('New client name').fill('laptop');
      await page.screenshot({
        path: testInfo.outputPath(`native-${launcherName}-signin-client-${width}.png`),
        fullPage: true,
      });
      await setup.getByRole('button', { name: 'Get sign-in command', exact: true }).click();
      await expect
        .poll(() => mutations[0]?.body)
        .toMatchObject({ shared_action: 'create', name: 'laptop' });

      const launcher = (token: string) =>
        `term-llm proxy run ${launcherName} --url '${url}' --token '${token}'`;
      const operatorLauncher = `${launcher(operatorToken)}${agy ? ' -- acp --login' : ''}`;
      const operatorCommand = page.getByLabel('Operator sign-in command', { exact: true });
      const workerCommand = page.getByLabel('Launcher command', { exact: true });
      await expect(operatorCommand).toHaveValue(operatorLauncher);
      await expect(operatorCommand).toBeFocused();
      await expect(setup).toContainText('Start sign-in within 15 minutes.');
      await expect(workerCommand).toHaveCount(0);
      await expect(page.getByLabel('Provider YAML')).toHaveCount(0);
      const copyOperator = page.getByRole('button', { name: 'Copy sign-in command' });
      await expect(copyOperator).toHaveClass('primary');
      await copyOperator.click();
      const nativeToast = page.locator('.toast-region').getByRole('status');
      await expect(nativeToast).toContainText('Operator sign-in command copied');
      expect(
        await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage })),
      ).not.toContain('scoped-browser-canary');
      expect(page.url()).not.toContain('scoped-browser-canary');
      page.once('dialog', (dialog) => dialog.dismiss());
      await page.getByRole('button', { name: 'Refresh state', exact: true }).click();
      await expect(operatorCommand).toHaveValue(operatorLauncher);
      await page.getByRole('button', { name: 'I saved it — dismiss' }).click();
      await expect(operatorCommand).toHaveCount(0);

      const detail = await openProvider(page, kind);
      const prefixCode = providerRoutePrefix(detail, kind);
      await expect(detail).toHaveAccessibleName(`${kind} (${provider})`);
      await expect(detail.getByRole('heading', { name: kind, exact: true })).toBeVisible();
      await expect(prefixCode).toHaveText(prefix);
      await expect(detail.getByRole('img', { name: 'Status: red, Sign-in needed' })).toBeVisible();
      await expect(detail.getByText(upstream, { exact: true })).toBeHidden();
      await expect(detail).not.toContainText('scoped-browser-canary');
      await expect(page.locator('.account-controls', { hasText: 'native' })).toHaveCount(0);

      const shared = detail.locator('.provider-account-section');
      await expect(
        shared.getByRole('heading', { name: 'Shared subscription', exact: true }),
      ).toBeVisible();
      await expect(
        shared.getByRole('button', { name: 'Sign in to shared subscription', exact: true }),
      ).toBeVisible();
      const addDevice = shared.getByRole('button', { name: 'Add device', exact: true });
      await expect(addDevice).toHaveCount(0);
      // The operator finishes sign-in outside this browser UI.
      state.accounts![0].state = 'ready';
      state.providers[0].status = 'green';
      state.providers[0].reason = '';
      await page.getByRole('button', { name: 'Refresh state', exact: true }).click();
      await expect(detail.getByRole('img', { name: 'Status: green' })).toBeVisible();
      await addDevice.click();
      await expect(workerCommand).toHaveValue(launcher(workerToken));
      await expect(operatorCommand).toHaveCount(0);
      await expect(shared).toContainText('does not open a browser');
      await page.getByRole('button', { name: 'Copy launcher command', exact: true }).click();
      await expect(nativeToast).toContainText('Launcher command copied');
      await expect(shared.locator('.native-integration').getByLabel('Provider YAML')).toBeVisible();
      await page.getByRole('button', { name: 'Copy provider YAML' }).click();
      const copies = await page.evaluate(
        () => (window as unknown as { proxyCopies: string[] }).proxyCopies,
      );
      expect(copies).toEqual([
        operatorLauncher,
        launcher(workerToken),
        `providers:\n  ${launcherName}-bin:\n    proxy:\n      url: '${url}'\n      token: '${workerToken}'\n`,
      ]);
      await page.getByRole('button', { name: 'Add provider', exact: true }).click();
      await expect(workerCommand).toHaveValue(launcher(workerToken));
      await expect(page.locator('.route-picker')).toHaveCount(0);
      await page.screenshot({
        path: testInfo.outputPath(`native-${launcherName}-setup-${width}.png`),
        fullPage: true,
      });
      await page.getByRole('button', { name: 'I saved it — dismiss' }).click();
      await expect(workerCommand).toHaveCount(0);
      await expect(
        detail.locator('.route-access-clients').getByText(`${shortName} worker`, { exact: true }),
      ).toBeVisible();
      await page.getByRole('button', { name: 'Clients', exact: true }).click();
      await expect(page.getByText(`${shortName} worker`, { exact: true })).toBeVisible();
      await page.getByRole('button', { name: 'Providers', exact: true }).click();
      await openProvider(page, kind, 'settings');
      const removeProvider = detail.getByRole('button', {
        name: `Delete provider ${kind}`,
        exact: true,
      });
      page.once('dialog', async (dialog) => {
        expect(dialog.message()).toContain(`Removes route ${kind}`);
        expect(dialog.message()).toContain('Connected accounts stay');
        await dialog.dismiss();
      });
      await removeProvider.click();
      expect(mutations).toHaveLength(2);
      await expect(prefixCode).toBeVisible();
      page.once('dialog', (dialog) => dialog.accept());
      await removeProvider.click();
      await expect(
        page.getByRole('heading', { name: 'Add your first provider', exact: true }),
      ).toBeVisible();
      await expect(page.getByRole('status')).toContainText(`Deleted provider ${kind}`);
      expect(mutations).toEqual([
        { method: 'POST', body: { revision: 1, shared_action: 'create', name: 'laptop' } },
        { method: 'POST', body: { revision: 2, route: kind } },
        { method: 'DELETE', body: { revision: 3 } },
      ]);
      expect(legacyCalls).toEqual([]);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
        ),
      ).toBe(true);
    });
  }
}

function configuredClaudeState(mode: ProxyState['mode'] = 'ui'): ProxyState {
  return {
    ...snapshot,
    auth: 'token',
    mode,
    has_requests: true,
    callers: [
      {
        id: 'native-client',
        name: 'Native client',
        created: 1,
        revoked: false,
        grants: ['manual-claude'],
        created_by: '',
        external_id: '',
      },
    ],
    accounts: [
      {
        name: 'manual-slot',
        kind: 'claude-bin',
        auth_mode: 'shared_vault',
        state: 'ready',
        generation: 1,
        updated: 1,
      },
    ],
    routes: [
      {
        serves: [],
        profile: '',
        provider: 'manual-claude',
        name: 'manual-claude',
        account: 'manual-slot',
        local_prefix: '/manual/',
        upstream: 'https://api.anthropic.com',
        enabled: true,
        allow_insecure_http: false,
        caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
        set_headers: {},
      },
      {
        serves: [],
        profile: 'openai',
        provider: 'openai',
        name: 'openai',
        local_prefix: '/openai/',
        upstream: 'https://api.openai.com/v1',
        enabled: true,
        allow_insecure_http: false,
        caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
        set_headers: {},
      },
    ],
  };
}

for (const status of [409, 503]) {
  test(`Claude provider delete failure retains the route; leaving Access is blocked during launcher display (${status})`, async ({
    page,
  }) => {
    const state = configuredClaudeState();
    let deletes = 0;
    await page.route('http://proxy.test/_proxy/api/**', async (request) => {
      const path = new URL(request.request().url()).pathname;
      if (path.endsWith('/state')) return request.fulfill({ json: withSingletonProviders(state) });
      if (
        path === '/_proxy/api/providers/manual-claude' &&
        request.request().method() === 'DELETE'
      ) {
        deletes++;
        return request.fulfill({ status, json: { error: 'Claude delete unavailable.' } });
      }
      if (path === '/_proxy/api/claude/setup') {
        state.revision++;
        return request.fulfill({
          json: {
            url: 'https://proxy.test/manual/',
            token: 'scoped-failure-canary',
            caller_id: 'client',
            revision: state.revision,
          },
        });
      }
      return request.fallback();
    });
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    const detail = await openProvider(page, 'manual-claude');
    const prefixCode = providerRoutePrefix(detail, 'manual-claude');
    await detail.getByRole('button', { name: 'Add device', exact: true }).click();
    const command = detail.getByLabel('Launcher command', { exact: true });
    await expect(command).toHaveValue(/scoped-failure-canary/);
    const removeProvider = detail.getByRole('button', {
      name: 'Delete provider manual-claude',
      exact: true,
    });
    // The delete action lives in Settings, so blocking the tab switch keeps it
    // unreachable. Switching providers must not hide the launcher either.
    for (const control of [providerTab(detail, 'settings'), providerItem(page, 'openai')]) {
      await control.click();
      await expect(page.getByRole('status')).toHaveText(
        'Finish saving or dismissing the one-time connection details first.',
      );
      await expect(command).toBeVisible();
      await expect(command).toHaveValue(/scoped-failure-canary/);
      await expect(providerTab(detail, 'access')).toHaveAttribute('aria-selected', 'true');
      await expect(providerItem(page, 'manual-claude')).toHaveAttribute('aria-current', 'true');
      await expect(removeProvider).toBeHidden();
      expect(deletes).toBe(0);
    }
    await detail.getByRole('button', { name: 'I saved it — dismiss' }).click();
    await expect(command).toHaveCount(0);
    await openProvider(page, 'manual-claude', 'settings');
    page.once('dialog', (dialog) => dialog.accept());
    await removeProvider.click();
    await expect(page.getByRole('status')).toContainText(
      status === 409 ? 'Claude delete unavailable.' : '503',
    );
    await expect(prefixCode).toBeVisible();
    await expect(providerTab(detail, 'settings')).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('status')).not.toContainText('cleared');
    expect(deletes).toBe(1);
  });
}

for (const width of [1440, 390]) {
  test(`Claude file-managed explanation has no mutation controls at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.route('http://proxy.test/_proxy/api/state', (request) =>
      request.fulfill({ json: withSingletonProviders(configuredClaudeState('file')) }),
    );
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await expect(page.getByText(/Routes are file-managed and read-only/)).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add provider', exact: true })).toHaveCount(0);
    await expect(providerItem(page, 'openai')).toBeVisible();
    const detail = await openProvider(page, 'manual-claude');
    await expect(providerRoutePrefix(detail, 'manual-claude')).toHaveText('/manual/');
    await expect(
      detail.getByRole('button', { name: 'Add device', includeHidden: true }),
    ).toHaveCount(0);
    await expect(providerTab(detail, 'settings')).toHaveCount(0);
    await openProvider(page, 'manual-claude', 'connection');
    await expect(
      detail.getByRole('button', { name: 'Edit manual-claude', exact: true }),
    ).toHaveCount(0);
    await expect(detail.locator('.account-controls')).toHaveCount(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
  });
}

for (const width of [1440, 390]) {
  test(`Providers stays focused and Admin owns configuration at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const configured: ProxyState = {
      ...snapshot,
      has_requests: true,
      accounts: [
        {
          name: 'claude',
          kind: 'claude-bin',
          state: 'ready',
          auth_mode: 'shared_vault',
          generation: 1,
          updated: 1,
        },
      ],
      routes: [
        {
          serves: [],
          profile: '',
          provider: 'claude',
          name: 'claude',
          local_prefix: '/claude/',
          allow_insecure_http: false,
          upstream: 'https://api.anthropic.com',
          enabled: true,
          account: 'claude',
          billing: 'plan',
          caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
          set_headers: {},
        },
      ],
      presets: [
        {
          family: 'inference',
          id: 'openai',
          label: 'OpenAI',
          upstream: 'https://api.openai.com/v1',
          auth: { header: 'Authorization', prefix: 'Bearer ' },
          billing: 'pay_as_you_go',
        },
        {
          family: 'inference',
          id: 'long-label',
          label: 'ChatGPT OAuth embeddings and live — billing not specified',
          upstream: 'https://example.invalid',
          auth: { header: 'Authorization', prefix: 'Bearer ' },
        },
      ],
    };
    await page.route('http://proxy.test/_proxy/api/state', (route) =>
      route.fulfill({ json: withSingletonProviders(configured) }),
    );
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
    await expect(page.locator('.onboarding')).toHaveCount(0);
    await expect(page.locator('.preset-pill')).toHaveCount(0);
    const navigationWidths = await page
      .getByRole('navigation', { name: 'Proxy administration', exact: true })
      .getByRole('button')
      .evaluateAll((nodes) =>
        nodes.map((node) => ({ visible: node.clientWidth, content: node.scrollWidth })),
      );
    for (const size of navigationWidths) expect(size.content).toBeLessThanOrEqual(size.visible + 1);
    await expect(page.getByRole('button', { name: 'Download routes.yaml' })).toHaveCount(0);
    const detail = await openProvider(page, 'claude', 'settings');
    await expect(
      detail.getByRole('button', { name: 'Delete provider claude', exact: true }),
    ).toBeVisible();
    await providerTab(detail, 'connection').click();
    await expect(detail.getByRole('button', { name: 'Edit claude', exact: true })).toBeVisible();
    await expect(
      detail.getByRole('button', { name: 'Add Claude account', exact: true, includeHidden: true }),
    ).toHaveCount(0);
    await choosePreset(page, 'OpenAI');
    await expect(page.getByLabel('Provider name', { exact: true })).toHaveValue('openai');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`routes-focused-${width}.png`),
      fullPage: true,
    });
    await page.getByRole('button', { name: 'Admin', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Admin', exact: true })).toBeVisible();
    await expect(page).toHaveURL(/tab=admin/);
    await expect(
      page.getByRole('button', { name: 'Download routes.yaml', exact: true }),
    ).toBeVisible();
    await page.getByLabel('Import source', { exact: true }).selectOption('paste');
    const yaml = page.getByLabel('Route configuration YAML', { exact: true });
    await expect(yaml).toBeVisible();
    expect(await yaml.evaluate((node) => node.closest('details'))).toBeNull();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath(`admin-config-${width}.png`),
      fullPage: true,
    });
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Admin', exact: true })).toBeVisible();
  });
}

for (const { id, label, issuer, verificationURL, upstream } of [
  {
    id: 'chatgpt',
    label: 'ChatGPT',
    issuer: 'OpenAI',
    verificationURL: 'https://auth.openai.com/codex/device',
    upstream: 'https://chatgpt.com/backend-api/codex',
  },
  {
    id: 'grok',
    label: 'Grok',
    issuer: 'xAI',
    verificationURL: 'https://accounts.x.ai/device',
    upstream: 'https://cli-chat-proxy.grok.com/v1',
  },
] as const) {
  test(`${label} sign-in creates a route before approving the provider`, async ({ page }) => {
    const providerRoute = {
      serves: [],
      profile: id,
      provider: id,
      name: id,
      local_prefix: `/${id}/`,
      upstream,
      enabled: true,
      allow_insecure_http: false,
      caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
      set_headers: {},
    };
    const state: ProxyState = {
      ...snapshot,
      auth: 'token',
      accounts: [],
      routes: [
        {
          ...providerRoute,
          provider: 'legacy',
          profile: '',
          serves: [],
          name: 'legacy',
          local_prefix: '/legacy/',
          upstream: 'https://example.test/v1',
        },
      ],
      providers: [
        {
          family: 'inference',
          status: 'green',
          reason: '',
          id: 'legacy',
          label: 'legacy',
          routes: ['legacy'],
        },
      ],
      callers: [
        {
          id: 'desktop',
          name: 'Desktop',
          grants: ['legacy'],
          created: 1,
          revoked: false,
          created_by: '',
          external_id: '',
        },
      ],
      presets: [
        {
          family: 'inference',
          id,
          label,
          upstream: providerRoute.upstream,
          auth: providerRoute.caller_auth,
          account_kind: id,
        },
      ],
    };
    let approved = false;
    const approvals: unknown[] = [];
    await page.route('http://proxy.test/_proxy/api/**', async (request) => {
      const url = new URL(request.request().url());
      const method = request.request().method();
      if (url.pathname.endsWith('/state'))
        return request.fulfill({ json: withSingletonProviders(state) });
      if (url.pathname === `/_proxy/api/${id}/setup`) {
        if (method === 'POST') {
          expect(request.request().postDataJSON()).toEqual({});
          return request.fulfill({
            json: {
              id: 'mock-attempt',
              user_code: 'TEST-1234',
              verification_url: verificationURL,
            },
          });
        }
        expect(method).toBe('GET');
        expect(url.searchParams.get('id')).toBe('mock-attempt');
        if (!approved) return request.fulfill({ json: { status: 'pending' } });
        state.routes.push({ ...providerRoute, account: `${id}-account` });
        state.providers.push({
          family: 'inference',
          status: 'green',
          reason: '',
          id,
          label,
          routes: [id],
        });
        state.accounts = [
          { name: `${id}-account`, kind: id, state: 'ready', generation: 1, updated: 1 },
        ];
        state.revision = 2;
        return request.fulfill({ json: { status: 'connected', route: providerRoute.name } });
      }
      if (url.pathname.endsWith('/callers') && method === 'PUT') {
        const body = request.request().postDataJSON() as {
          id: string;
          provider_changes: { id: string; approved: boolean }[];
          revision: number;
        };
        approvals.push(body);
        expect(body.provider_changes).toEqual([{ id, approved: true }]);
        expect(body).not.toHaveProperty('grants');
        if (!state.callers[0].grants.includes(providerRoute.name))
          state.callers[0].grants.push(providerRoute.name);
        state.revision = 3;
        return request.fulfill({ json: { ok: true } });
      }
      return request.fallback();
    });
    await page.goto('http://proxy.test/_proxy/?tab=routes');
    await expect(providerItem(page, 'legacy')).toHaveAttribute('aria-current', 'true');
    await choosePreset(page, label);
    await expect(page.getByRole('heading', { name: `Set up ${label}`, exact: true })).toBeVisible();
    for (const field of [
      'API key',
      'Credential source',
      'Trusted account',
      'Paste credential JSON (write-only)',
    ]) {
      await expect(page.getByLabel(field, { exact: true })).toHaveCount(0);
    }
    await page.getByRole('button', { name: `Sign in to ${label}`, exact: true }).click();
    const code = page.getByLabel('One-time sign-in code', { exact: true });
    await expect(code).toHaveValue('TEST-1234');
    await expect(
      page.getByRole('link', { name: `Continue to ${issuer}`, exact: true }),
    ).toHaveAttribute('href', verificationURL);
    const item = providerItem(page, id);
    await expect(item).toHaveCount(0);
    approved = true;
    // Wait for route-focused refresh to select the new provider without clicking it.
    const detail = page.locator(`#provider-detail[data-provider="${id}"]`);
    const prefix = providerRoutePrefix(detail, id);
    await expect(prefix).toBeFocused();
    await expect(item).toHaveAttribute('aria-current', 'true');
    await expect(detail.getByRole('img', { name: 'Status: green' })).toBeVisible();
    await expect(detail.locator('.route-access-empty')).toHaveText('No clients approved yet.');
    await expect(item.locator('.provider-list-count')).toHaveText('0');
    await expect(code).toHaveCount(0);
    expect(approvals).toEqual([]);
    const access = detail.locator('.route-access');
    await access.getByLabel('Desktop', { exact: true }).check();
    await access.getByRole('button', { name: 'Approve selected', exact: true }).click();
    await expect(access.locator('.route-access-client')).toContainText('Desktop');
    await expect(item.locator('.provider-list-count')).toHaveText('1');
    await expect(prefix).toHaveText(`/${id}/`);
    expect(state.callers[0].grants).toEqual(['legacy', id]);
    expect(approvals).toEqual([
      {
        id: 'desktop',
        provider_changes: [{ id, approved: true }],
        revision: 2,
      },
    ]);
  });
}

test('Provider approval creates one show-once client for every current route', async ({ page }) => {
  const routeDefaults = {
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  };
  const routes = [
    {
      ...routeDefaults,
      provider: 'alpha',
      profile: '',
      serves: [],
      name: 'alpha',
      local_prefix: '/alpha/',
      upstream: 'https://alpha.example.test/v1',
    },
    {
      ...routeDefaults,
      provider: 'alpha',
      profile: '',
      serves: [],
      name: 'alpha-extra',
      local_prefix: '/alpha-extra/',
      upstream: 'https://alpha.example.test/extra',
    },
  ];
  const state: ProxyState = {
    ...snapshot,
    auth: 'token',
    revision: 4,
    routes,
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'alpha',
        label: 'Alpha',
        routes: ['alpha', 'alpha-extra'],
      },
    ],
    callers: [],
  };
  const creations: unknown[] = [];
  await page.route('http://proxy.test/_proxy/api/**', async (request) => {
    const url = new URL(request.request().url());
    const method = request.request().method();
    if (url.pathname.endsWith('/state'))
      return request.fulfill({ json: withSingletonProviders(state) });
    if (url.pathname.endsWith('/callers') && method === 'POST') {
      const body = request.request().postDataJSON() as {
        name: string;
        providers: string[];
        revision: number;
      };
      creations.push(body);
      const caller = {
        id: 'worker',
        name: body.name,
        grants: [...routes.map((route) => route.name)],
        revoked: false,
        created: 1,
        created_by: '',
        external_id: '',
      };
      state.callers = [caller];
      state.revision++;
      return request.fulfill({
        status: 201,
        json: { token: 'provider-show-once-token', caller },
      });
    }
    return request.fallback();
  });
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  const detail = await openProvider(page, 'alpha');
  const item = providerItem(page, 'alpha');
  await expect(item).toHaveAttribute('data-routes', 'alpha alpha-extra');
  await expect(item.locator('.provider-list-count')).toHaveText('0');
  const access = detail.locator('.route-access');
  await expect(access).toHaveCount(1);
  await expect(access.locator('.route-access-empty')).toHaveText('No clients approved yet.');
  await expect(access.locator('.route-access-create')).toHaveCount(1);
  await access.getByLabel('Client name', { exact: true }).fill('Worker');
  await access.getByRole('button', { name: 'Create client', exact: true }).click();
  const token = access.getByLabel('Client token', { exact: true });
  await expect(token).toHaveValue('provider-show-once-token');
  await expect(token).toBeFocused();
  await expect(page.getByRole('status')).toContainText('shown once');
  expect(creations).toEqual([{ name: 'Worker', providers: ['alpha'], revision: 4 }]);
  for (const route of routes) {
    await expect(
      access.getByText(`Base URL: http://proxy.test${route.local_prefix}`, { exact: false }),
    ).toBeVisible();
  }
  await expect(access.getByRole('button', { name: 'Create client', exact: true })).toHaveCount(0);
  await access.getByRole('button', { name: 'I saved it — dismiss' }).click();
  await expect(token).toHaveCount(0);
  await expect(access.locator('.route-access-client')).toContainText('Worker');
  await expect(item.locator('.provider-list-count')).toHaveText('1');
});

test('Providers group ChatGPT routes under one OAuth account and keep OpenAI routes separate', async ({
  page,
}, testInfo) => {
  const routeDefaults = {
    enabled: true,
    allow_insecure_http: false,
    caller_auth: { header: 'Authorization', prefix: 'Bearer ' },
    set_headers: {},
  };
  const chatgptRoutes = [
    {
      ...routeDefaults,
      provider: 'chatgpt',
      profile: 'chatgpt',
      serves: ['providers.chatgpt.base_url'],
      name: 'chatgpt',
      account: 'chatgpt-work',
      local_prefix: '/chatgpt/',
      upstream: 'https://chatgpt.com/backend-api/codex',
      billing: 'plan' as const,
    },
    {
      ...routeDefaults,
      provider: 'chatgpt',
      profile: 'chatgpt-backend',
      serves: ['transcription: chatgpt'],
      name: 'chatgpt-backend',
      account: 'chatgpt-work',
      local_prefix: '/chatgpt-backend/',
      upstream: 'https://chatgpt.com/backend-api',
      billing: 'plan' as const,
    },
    {
      ...routeDefaults,
      provider: 'chatgpt',
      profile: 'chatgpt-openai',
      serves: ['embed: chatgpt'],
      name: 'chatgpt-openai',
      account: 'chatgpt-work',
      local_prefix: '/chatgpt-openai/',
      upstream: 'https://api.openai.com/v1',
      billing: 'unspecified' as const,
    },
  ];
  const openaiRoute = {
    ...routeDefaults,
    provider: 'openai',
    profile: 'openai',
    serves: [],
    name: 'openai',
    local_prefix: '/openai/',
    upstream: 'https://api.openai.com/v1',
    billing: 'pay_as_you_go' as const,
    set_headers: { Authorization: { secret: 'openai-key', prefix: 'Bearer ' } },
  };
  const state: ProxyState = {
    ...snapshot,
    auth: 'token',
    revision: 7,
    accounts: [
      { name: 'chatgpt-work', kind: 'chatgpt', state: 'ready', generation: 1, updated: 1 },
    ],
    routes: [...chatgptRoutes, openaiRoute],
    providers: [
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'chatgpt',
        label: 'ChatGPT',
        routes: ['chatgpt', 'chatgpt-backend', 'chatgpt-openai'],
      },
      {
        family: 'inference',
        status: 'green',
        reason: '',
        id: 'openai',
        label: 'OpenAI',
        routes: ['openai'],
      },
    ],
    secrets: [{ name: 'openai-key', routes: ['openai'], source: 'stored' }],
    callers: [
      {
        id: 'jarvis',
        name: 'jarvis',
        grants: ['chatgpt', 'chatgpt-backend'],
        revoked: false,
        created: 1,
        created_by: '',
        external_id: '',
      },
      {
        id: 'forge',
        name: 'forge',
        grants: ['openai'],
        revoked: false,
        created: 1,
        created_by: '',
        external_id: '',
      },
    ],
    presets: [
      {
        family: 'inference',
        id: 'chatgpt',
        label: 'ChatGPT',
        upstream: 'https://chatgpt.com/backend-api/codex',
        auth: { header: 'Authorization', prefix: 'Bearer ' },
        account_kind: 'chatgpt',
        billing: 'plan',
        companions: ['chatgpt-backend', 'chatgpt-openai'],
      },
      {
        family: 'inference',
        id: 'chatgpt-backend',
        label: 'ChatGPT Usage & Dictation',
        upstream: 'https://chatgpt.com/backend-api',
        auth: { header: 'Authorization', prefix: 'Bearer ' },
        account_kind: 'chatgpt',
        billing: 'plan',
      },
      {
        family: 'inference',
        id: 'chatgpt-openai',
        label: 'ChatGPT Embeddings & Live',
        upstream: 'https://api.openai.com/v1',
        auth: { header: 'Authorization', prefix: 'Bearer ' },
        account_kind: 'chatgpt',
        billing: 'unspecified',
      },
      {
        family: 'inference',
        id: 'openai',
        label: 'OpenAI',
        upstream: 'https://api.openai.com/v1',
        auth: { header: 'Authorization', prefix: 'Bearer ' },
        billing: 'pay_as_you_go',
      },
    ],
  };
  const approvals: {
    id: string;
    provider_changes: { id: string; approved: boolean }[];
    revision: number;
  }[] = [];
  await page.route('http://proxy.test/_proxy/api/**', async (request) => {
    const url = new URL(request.request().url());
    const method = request.request().method();
    if (url.pathname.endsWith('/state'))
      return request.fulfill({ json: withSingletonProviders(state) });
    if (url.pathname.endsWith('/callers') && method === 'PUT') {
      const body = request.request().postDataJSON() as {
        id: string;
        provider_changes: { id: string; approved: boolean }[];
        revision: number;
      };
      approvals.push(body);
      expect(body).not.toHaveProperty('grants');
      const caller = state.callers.find((value) => value.id === body.id)!;
      for (const change of body.provider_changes) {
        const provider = state.providers!.find((value) => value.id === change.id)!;
        caller.grants = change.approved
          ? [...new Set([...caller.grants, ...provider.routes])]
          : caller.grants.filter((route) => !provider.routes.includes(route));
      }
      state.revision++;
      return request.fulfill({ json: { ok: true } });
    }
    return request.fallback();
  });
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  await expect(page.getByRole('button', { name: 'Providers', exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
  const list = page.getByRole('navigation', { name: 'Providers', exact: true });
  await expect(list.locator('.provider-list-name')).toHaveText(['chatgpt', 'openai']);
  const chatgptItem = providerItem(page, 'chatgpt');
  const openaiItem = providerItem(page, 'openai');
  await expect(chatgptItem).toHaveAttribute(
    'data-routes',
    'chatgpt chatgpt-backend chatgpt-openai',
  );
  await expect(openaiItem).toHaveAttribute('data-routes', 'openai');
  // Partial grants count as access, without implying full approval.
  await expect(chatgptItem).toHaveAccessibleName(
    'chatgpt (ChatGPT), 1 client with access, 1 needs approval',
  );
  await expect(openaiItem).toHaveAccessibleName('openai (OpenAI), 1 client with access');
  await expect(chatgptItem).toHaveAttribute('aria-current', 'true');
  const chatgpt = page.locator('#provider-detail[data-provider="chatgpt"]');
  await expect(chatgpt).toHaveAccessibleName('chatgpt (ChatGPT)');
  await expect(chatgpt.locator('.provider-detail-meta')).toContainText('Account chatgpt-work');
  await expect(providerTab(chatgpt, 'access')).toHaveAttribute('aria-selected', 'true');
  await expect(chatgpt.getByRole('tabpanel')).toHaveCount(1);
  await expect(chatgpt.locator('.provider-routes-section')).toBeHidden();
  await choosePreset(page, 'OpenAI');
  await expect(page.getByRole('heading', { name: 'Set up OpenAI', exact: true })).toBeVisible();
  await expect(page.getByLabel('Label', { exact: true })).toHaveValue('OpenAI');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();

  await expect(chatgpt.locator('.provider-detail-routes code')).toHaveText(
    chatgptRoutes.map((route) => route.local_prefix),
  );
  await providerTab(chatgpt, 'connection').click();
  const connection = chatgpt.getByRole('tabpanel');
  // The OAuth account is represented once even though three routes use it.
  await expect(connection.getByRole('heading', { name: 'Account', exact: true })).toHaveCount(1);
  await expect(connection.locator('.account-controls')).toHaveCount(1);
  await expect(connection.locator('.account-controls-status')).toContainText('chatgpt-work');
  await expect(
    connection.getByRole('button', { name: 'Reconnect ChatGPT', exact: true }),
  ).toBeVisible();
  const panels = connection.locator('.provider-route-management');
  await expect(panels).toHaveCount(3);
  for (const route of chatgptRoutes) {
    await expect(providerRoutePrefix(chatgpt, route.name)).toHaveText(route.local_prefix);
    const panel = connection.locator(`.provider-route-management[data-route="${route.name}"]`);
    await expect(panel.locator('.provider-route-header code')).toHaveText(route.local_prefix);
    await expect(routeFact(panel, /^term-llm uses it for$/)).toHaveText(route.serves.join(' · '));
    await expect(
      panel.getByRole('button', { name: `Edit ${route.name}`, exact: true }),
    ).toBeVisible();
  }
  // Connection describes routes only; approval controls and client names belong to Access.
  await expect(connection.locator('.route-access')).toHaveCount(0);
  await expect(connection).not.toContainText(/jarvis|forge/);
  await expect(routeFact(connection, /^Billing$/)).toHaveText([
    'Subscription',
    'Subscription',
    'Not specified',
  ]);
  await providerTab(chatgpt, 'settings').click();
  await expect(
    chatgpt.getByRole('button', { name: 'Delete provider chatgpt', exact: true }),
  ).toBeVisible();

  await providerTab(chatgpt, 'access').click();
  const access = chatgpt.locator('.route-access');
  await expect(access).toHaveCount(1);
  await expect(access.getByRole('heading', { name: /^Clients with access\s*1$/ })).toBeVisible();
  const current = access.locator('.route-access-clients');
  await expect(current.locator('.route-access-client')).toHaveCount(1);
  await expect(current).toContainText('jarvis');
  await expect(current.locator('.route-access-pending')).toHaveText('Needs approval');
  const grantForm = access.locator('.route-access-grant');
  await grantForm.getByLabel('jarvis (needs approval)', { exact: true }).check();
  await grantForm.getByRole('button', { name: 'Approve selected', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Approved jarvis for chatgpt');
  await expect(providerTab(chatgpt, 'access')).toHaveAttribute('aria-selected', 'true');
  await expect(current.locator('.route-access-client')).toHaveCount(1);
  await expect(current).toContainText('jarvis');
  await expect(current.locator('.route-access-pending')).toHaveCount(0);
  expect(approvals).toEqual([
    { id: 'jarvis', provider_changes: [{ id: 'chatgpt', approved: true }], revision: 7 },
  ]);
  // Approval covers every current ChatGPT route and never the separate OpenAI provider.
  expect(state.callers[0].grants).toEqual(chatgptRoutes.map((route) => route.name));
  const openai = await openProvider(page, 'openai');
  await expect(chatgpt).toHaveCount(0);
  await expect(openai).toHaveAccessibleName('openai (OpenAI)');
  await expect(openai.locator('.provider-detail-meta')).toContainText('API key openai-key');
  await expect(openai.locator('.route-access-client')).toHaveCount(1);
  await expect(openai.locator('.route-access-client')).toContainText('forge');
  await providerTab(openai, 'connection').click();
  await page.getByRole('button', { name: 'Refresh state', exact: true }).click();
  await expect(page.locator('.providers-intro').getByRole('heading')).toBeFocused();
  await expect(openaiItem).toHaveAttribute('aria-current', 'true');
  await expect(providerTab(openai, 'connection')).toHaveAttribute('aria-selected', 'true');
  const openaiConnection = openai.getByRole('tabpanel');
  await expect(openaiConnection.locator('.account-controls')).toHaveCount(0);
  await expect(
    openaiConnection.getByRole('button', { name: 'Reconnect ChatGPT', exact: true }),
  ).toHaveCount(0);
  await expect(
    openaiConnection.getByRole('button', { name: 'Configure API key', exact: true }),
  ).toBeVisible();
  await expect(routeFact(openaiConnection, /^Billing$/)).toHaveText('Pay as you go');
  await providerTab(openai, 'settings').click();
  await expect(
    openai.getByRole('button', { name: 'Delete provider openai', exact: true }),
  ).toBeVisible();
  await openProvider(page, 'chatgpt', 'connection');
  await expect(panels).toHaveCount(3);
  await expect(connection).not.toContainText(/jarvis|forge/);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('providers-grouped.png'), fullPage: true });
});

test('Providers consistently offers one Add provider action before and after recorded requests', async ({
  page,
}) => {
  let hasRequests = false;
  await page.route('http://proxy.test/_proxy/api/state', (route) =>
    route.fulfill({ json: { ...snapshot, has_requests: hasRequests } }),
  );
  await page.goto('http://proxy.test/_proxy/?tab=routes');
  const add = page.getByRole('button', { name: 'Add provider', exact: true });
  await expect(add).toHaveCount(1);
  await expect(page.locator('.onboarding')).toHaveCount(0);
  hasRequests = true;
  await page.getByRole('button', { name: 'Refresh state', exact: true }).click();
  await expect(add).toHaveCount(1);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Providers', exact: true })).toBeVisible();
  await expect(add).toHaveCount(1);
});

test('Usage matrix keeps every quota window visible without a disclosure', async ({
  page,
}, info) => {
  await page.route('http://proxy.test/_proxy/api/state', (route) =>
    route.fulfill({ json: { ...snapshot, has_native_usage: true } }),
  );
  const now = Date.now();
  const limit = (label: string, minutes: number, used: number, elapsed: number) => ({
    label,
    duration_minutes: minutes,
    used_percent: used,
    resets_at: new Date(now + minutes * (1 - elapsed) * 60000).toISOString(),
  });
  const updated = new Date(now).toISOString();
  await page.route('http://proxy.test/_proxy/api/usage', (route) =>
    route.fulfill({
      json: {
        interval_minutes: 15,
        refreshing: false,
        providers: [
          {
            id: 'agy-bin',
            updated_at: updated,
            report: {
              groups: [
                {
                  name: 'Gemini Models',
                  detail:
                    'Gemini 4 Neon · Gemini 4 Hydrogen · Orchestrator (Neon) · Gemini 3.8 Flash',
                  windows: [limit('5 hours', 300, 34, 0.94), limit('Weekly', 10080, 10, 0.03)],
                },
                {
                  name: 'Claude and GPT models',
                  windows: [limit('6 hours', 360, 8, 0.16), limit('Weekly', 10080, 75, 0.6)],
                },
                { name: 'Gemini 4 Argon', windows: [limit('Weekly', 10080, 0, 0)] },
              ],
            },
          },
          {
            id: 'chatgpt',
            route: 'work-plan',
            updated_at: updated,
            report: {
              plan: 'Pro',
              groups: [
                {
                  name: 'Codex',
                  windows: [limit('Weekly', 10080, 71, 0.83), limit('5 hours', 300, 100, 0.05)],
                },
              ],
            },
          },
          {
            id: 'chatgpt',
            route: 'personal-plan',
            error: 'Provider usage is temporarily unavailable',
          },
        ],
      },
    }),
  );
  await page.goto('http://proxy.test/_proxy/?tab=usage');
  const table = page.getByRole('table');
  await expect(table.locator('tbody tr')).toHaveCount(8);
  await expect(page.getByText('2 providers · 7 limits')).toBeVisible();
  await expect(table.getByText('Limit reached')).toBeVisible();
  await expect(table.getByText('personal-plan')).toBeVisible();
  await expect(table.getByText('work-plan')).toBeVisible();
  for (const name of [
    'Gemini Models',
    'Gemini 4 Argon',
    'Orchestrator (Neon)',
    'Claude and GPT models',
    'Codex',
  ]) {
    await expect(table.getByText(name, { exact: false }).first()).toBeVisible();
  }
  await expect(table.getByText(/Runs out .*early/i)).toBeVisible();
  await expect(table.locator('details')).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: info.outputPath('quota-matrix.png'), fullPage: true });
});
