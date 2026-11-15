import {
  expect,
  request,
  test,
  type BrowserContext,
  type CDPSession,
  type Page,
} from '@playwright/test';
import {
  closeSync,
  constants,
  fstatSync,
  ftruncateSync,
  lstatSync,
  mkdirSync,
  openSync,
  readSync,
  writeFileSync,
} from 'node:fs';
import { join, resolve } from 'node:path';

const configuredURL = process.env.TERM_LLM_PROXY_PASSKEY_URL || '';
const state = process.env.TERM_LLM_PROXY_PASSKEY_STATE
  ? resolve(process.env.TERM_LLM_PROXY_PASSKEY_STATE)
  : '';
const phase = process.env.TERM_LLM_PROXY_PASSKEY_PHASE || 'enroll';

// External endpoint only. In particular, override the shared config's TLS bypass.
// Synthetic CDP credentials do not test hardware, attestation or public CA issuance.
// Disable recordings: fixture secrets and credential material must stay in private STATE files.
test.use({
  ignoreHTTPSErrors: false,
  trace: 'off',
  video: 'off',
  screenshot: 'off',
  actionTimeout: 10_000,
  navigationTimeout: 20_000,
});
test.skip(!configuredURL || !state, 'run scripts/proxy_passkey_smoke.sh with an external fixture');
test.setTimeout(90_000);

function proxyURL(value: string): URL {
  const url = new URL(value);
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !['/', '/_proxy', '/_proxy/'].includes(url.pathname) ||
    !(url.protocol === 'https:' || (url.protocol === 'http:' && url.hostname === 'localhost'))
  )
    throw new Error('Use an HTTPS origin or /_proxy/ URL (HTTP is allowed only on localhost).');
  url.pathname = '/_proxy/';
  return url;
}
function privateDirectory(path: string) {
  const existing = lstatSync(path, { throwIfNoEntry: false });
  if (!existing) mkdirSync(path, { mode: 0o700 });
  const directory = lstatSync(path);
  if (!directory.isDirectory() || directory.isSymbolicLink() || (directory.mode & 0o077) !== 0)
    throw new Error('STATE must be a private directory (0700 or stricter), not a symlink.');
}
function privateFile(path: string, limit: number, required = true) {
  const file = lstatSync(path, { throwIfNoEntry: false });
  if (!file && !required) return;
  if (!file?.isFile() || file.isSymbolicLink() || (file.mode & 0o077) !== 0 || file.size > limit)
    throw new Error('Fixture/output files must be bounded private regular files, not symlinks.');
  return file;
}
function readPrivate(path: string, limit: number): string {
  const expected = privateFile(path, limit)!;
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const file = fstatSync(fd);
    if (
      !file.isFile() ||
      (file.mode & 0o077) !== 0 ||
      file.size > limit ||
      file.ino !== expected.ino ||
      file.dev !== expected.dev
    )
      throw new Error('Unsafe fixture file.');
    const bytes = Buffer.alloc(limit + 1);
    const size = readSync(fd, bytes, 0, bytes.length, 0);
    if (!size || size > limit) throw new Error('Fixture file is empty or too large.');
    return bytes.subarray(0, size).toString('utf8');
  } finally {
    closeSync(fd);
  }
}
function privateSecret(path: string | undefined): string {
  if (!path) throw new Error('The phase requires an explicit private fixture secret file.');
  const value = readPrivate(path, 4096).trim();
  if (!value) throw new Error('The private fixture secret file is empty.');
  return value;
}
function savePrivate(name: string, value: unknown) {
  privateDirectory(state);
  const path = join(state, name);
  const existing = privateFile(path, 65_536, false);
  const fd = openSync(
    path,
    constants.O_WRONLY |
      constants.O_NOFOLLOW |
      constants.O_NONBLOCK |
      (existing ? 0 : constants.O_CREAT | constants.O_EXCL),
    0o600,
  );
  try {
    const file = fstatSync(fd);
    if (
      !file.isFile() ||
      (file.mode & 0o077) !== 0 ||
      (existing && (file.ino !== existing.ino || file.dev !== existing.dev))
    )
      throw new Error('Unsafe state output file.');
    const body = JSON.stringify(value);
    if (Buffer.byteLength(body) > 65_536)
      throw new Error('State output exceeds the fixture size bound.');
    ftruncateSync(fd, 0);
    writeFileSync(fd, body);
  } finally {
    closeSync(fd);
  }
}
async function virtualAuthenticator(cdp: CDPSession): Promise<string> {
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  return authenticatorId;
}
async function apiStatus(page: Page, base: URL): Promise<number> {
  // Probe through Chromium so the caller-provisioned browser trust store is used.
  return page.evaluate(async (url) => {
    const response = await fetch(url, {
      credentials: 'same-origin',
      redirect: 'error',
      signal: AbortSignal.timeout(10_000),
    });
    await response.body?.cancel();
    return response.status;
  }, new URL('api/state', base).href);
}
async function signedIn(page: Page) {
  await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Passkeys', exact: true })).toBeVisible();
}
async function signIn(page: Page) {
  await page.getByRole('button', { name: 'Sign in with passkey', exact: true }).click();
  await signedIn(page);
}
async function checkCookie(context: BrowserContext, base: URL) {
  const cookie = (await context.cookies(base.href)).find((entry) => entry.name === 'tlp_admin');
  // Never pass the entire cookie (including its value) to a potentially failing matcher.
  expect(Boolean(cookie), 'an administrator session cookie was issued').toBe(true);
  expect({
    httpOnly: cookie?.httpOnly,
    secure: cookie?.secure,
    sameSite: cookie?.sameSite,
    path: cookie?.path,
  }).toEqual({
    httpOnly: true,
    secure: base.protocol === 'https:',
    sameSite: 'Strict',
    path: '/_proxy/',
  });
}

test('Proxy passkeys: external enrollment, restart or recovery', async ({
  page,
  context,
  browser,
}, testInfo) => {
  if (!['enroll', 'restart', 'recovery'].includes(phase))
    throw new Error('PHASE must be enroll, restart or recovery.');
  const base = proxyURL(configuredURL);
  privateDirectory(state);
  privateDirectory(testInfo.project.outputDir);
  for (const name of ['browser.json', 'credentials.json'])
    privateFile(join(state, name), 65_536, false);
  const authorization: string[] = [];
  context.on('request', (outgoing) => {
    if (outgoing.headers().authorization)
      authorization.push('unexpected browser Authorization header');
  });
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  let authenticatorId = await virtualAuthenticator(cdp);
  if (phase !== 'enroll') {
    const saved = JSON.parse(readPrivate(join(state, 'browser.json'), 65_536));
    const credentials = JSON.parse(readPrivate(join(state, 'credentials.json'), 65_536));
    expect(credentials.length > 0, 'saved virtual credentials are present').toBe(true);
    await context.addCookies(saved.cookies);
    for (const credential of credentials)
      await cdp.send('WebAuthn.addCredential', { authenticatorId, credential });
    expect(
      (await context.cookies(base.href)).some((cookie) => cookie.name === 'tlp_admin'),
      'saved session cookie is present',
    ).toBe(true);
  }
  await page.goto(base.href, { timeout: 20_000 });
  if (phase === 'restart') {
    // Caller restarted the proxy; credentials persist but administrator sessions do not.
    expect(
      await apiStatus(page, base),
      'old session must fail after the caller restarts the proxy',
    ).toBe(401);
  }
  if (phase === 'enroll') {
    await expect(page.getByLabel('Bootstrap token', { exact: true })).toBeVisible();
    await page
      .getByLabel('Bootstrap token', { exact: true })
      .fill(privateSecret(process.env.TERM_LLM_PROXY_PASSKEY_BOOTSTRAP_FILE));
    await page.getByLabel('Passkey name', { exact: true }).fill('Proxy smoke original');
    await page.getByRole('button', { name: 'Create first passkey', exact: true }).click();
    await signedIn(page);
    await checkCookie(context, base);
    await page.getByRole('button', { name: 'Sign out', exact: true }).click();
    await expect(
      page.getByRole('button', { name: 'Sign in with passkey', exact: true }),
    ).toBeVisible();
    expect(await apiStatus(page, base)).toBe(401);
    expect((await context.cookies(base.href)).some((cookie) => cookie.name === 'tlp_admin')).toBe(
      false,
    );
    await signIn(page);
  } else if (phase === 'restart') {
    await signIn(page);
  } else {
    // Establish an old session even if the caller also restarted before recovery.
    if ((await apiStatus(page, base)) === 401) await signIn(page);
    await signedIn(page);
    const oldBrowser = await browser.newContext({ ignoreHTTPSErrors: false });
    try {
      await oldBrowser.addCookies(await context.cookies());
      const oldPage = await oldBrowser.newPage();
      await oldPage.goto(base.href, { timeout: 20_000 });
      expect(await apiStatus(oldPage, base)).toBe(200);
      // Lose the original authenticator entirely; recover using a fresh one and no cookies.
      await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId });
      authenticatorId = await virtualAuthenticator(cdp);
      await context.clearCookies();
      await page.goto(base.href, { timeout: 20_000 });
      await page.getByRole('button', { name: 'Recover access', exact: true }).click();
      await expect(page.getByLabel('Recovery token', { exact: true })).toBeVisible();
      await page
        .getByLabel('Recovery token', { exact: true })
        .fill(privateSecret(process.env.TERM_LLM_PROXY_PASSKEY_RECOVERY_FILE));
      await page.getByLabel('Passkey name', { exact: true }).fill('Proxy smoke recovery');
      await page.getByRole('button', { name: 'Recover with new passkey', exact: true }).click();
      await expect(
        page.getByRole('button', { name: 'Sign in with passkey', exact: true }),
      ).toBeVisible();
      expect(await apiStatus(page, base), 'recovery must not issue a session').toBe(401);
      expect((await context.cookies(base.href)).some((cookie) => cookie.name === 'tlp_admin')).toBe(
        false,
      );
      expect(
        await apiStatus(oldPage, base),
        'recovery must invalidate the existing browser session',
      ).toBe(401);
      await signIn(page);
    } finally {
      await oldBrowser.close();
    }
  }
  await checkCookie(context, base);
  expect(await apiStatus(page, base)).toBe(200);
  await page.getByRole('button', { name: 'Passkeys', exact: true }).click();
  await expect(
    page.getByRole('heading', {
      name: phase === 'recovery' ? 'Proxy smoke recovery' : 'Proxy smoke original',
      exact: true,
    }),
  ).toBeVisible();

  const routePath = process.env.TERM_LLM_PROXY_PASSKEY_ROUTE_PATH;
  const callerFile = process.env.TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE;
  if (routePath || callerFile) {
    if (!routePath?.startsWith('/') || routePath.startsWith('//') || !callerFile)
      throw new Error('Optional forwarding needs both ROUTE_PATH and CALLER_TOKEN_FILE.');
    const route = new URL(routePath, base.origin);
    if (route.origin !== base.origin || route.pathname.startsWith('/_proxy'))
      throw new Error('ROUTE_PATH must be a same-origin machine route, not administration.');
    const cookieOnlyStatus = await page.evaluate(async (url) => {
      const response = await fetch(url, {
        credentials: 'same-origin',
        redirect: 'error',
        signal: AbortSignal.timeout(10_000),
      });
      await response.body?.cancel();
      return response.status;
    }, route.href);
    expect(cookieOnlyStatus, 'signed-in browser cookies must not authorize forwarding').toBe(401);
    // A separate, empty API cookie jar: never copy browser cookies to machine requests.
    const machine = await request.newContext({
      ignoreHTTPSErrors: false,
      storageState: { cookies: [], origins: [] },
    });
    try {
      const response = await machine.get(route.href, {
        headers: { Authorization: `Bearer ${privateSecret(callerFile)}` },
        timeout: 10_000,
        maxRedirects: 0,
      });
      expect(response.ok(), 'synthetic forwarding with a caller header succeeds').toBe(true);
      await response.dispose();
    } finally {
      await machine.dispose();
    }
  }
  expect(authorization).toEqual([]);
  expect(
    await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })),
  ).toEqual({ local: 0, session: 0 });
  const { credentials } = await cdp.send('WebAuthn.getCredentials', { authenticatorId });
  expect(credentials.length > 0, 'the ceremony created a resident virtual credential').toBe(true);
  savePrivate('credentials.json', credentials);
  savePrivate('browser.json', { cookies: await context.cookies() });
});
