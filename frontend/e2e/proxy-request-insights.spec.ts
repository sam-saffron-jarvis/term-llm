import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { gunzipSync } from 'node:zlib';
import { expect, test } from '@playwright/test';
import type { ProxyRequest, ProxyState } from '../src/api/proxy-client';

// Actual built DOM/CSS, synthetic metadata only. No providers or real accounts.
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
  csrf: 'review-fixture',
};

function rows(): ProxyRequest[] {
  const started = new Date(2026, 10, 28, 12).getTime();
  const variants: { name: string; value: Partial<ProxyRequest> }[] = [
    {
      name: 'normal',
      value: {
        request_meta: {
          state: 'observed',
          model: 'requested-normal',
          reasoning_effort: 'high',
          output_limit: 0,
          stream: false,
        },
        response_meta: { state: 'observed', status: 'completed' },
      },
    },
    { name: 'warning', value: { outcome: 'incomplete' } },
    { name: 'unknown', value: { status: 0, outcome: 'unknown' } },
    {
      name: 'complete-turn',
      value: {
        status: 101,
        outcome: 'complete',
        websocket_connection_id: 'connection',
        response_meta: { state: 'observed', status: 'completed' },
      },
    },
    {
      name: 'limited-turn',
      value: {
        status: 101,
        outcome: 'incomplete',
        websocket_connection_id: 'connection',
        response_meta: {
          state: 'observed',
          status: 'incomplete',
          incomplete_reason: 'max_output_tokens',
        },
        money: { billing: 'pay_as_you_go', kind: 'unpriced', reason: 'Exact rate unavailable.' },
      },
    },
    {
      name: 'failed-turn',
      value: {
        status: 101,
        outcome: 'incomplete',
        websocket_connection_id: 'connection',
        response_meta: { state: 'observed', status: 'failed' },
      },
    },
    {
      name: 'cancelled-turn',
      value: {
        status: 101,
        outcome: 'incomplete',
        websocket_connection_id: 'connection',
        response_meta: { state: 'observed', status: 'cancelled' },
      },
    },
    {
      name: 'unknown-turn',
      value: { status: 101, outcome: 'unknown', websocket_connection_id: 'connection' },
    },
    { name: 'closed-connection', value: { status: 101, outcome: 'closed_by_client' } },
    { name: 'shutdown-connection', value: { status: 101, outcome: 'closed_by_shutdown' } },
    { name: 'revoked-connection', value: { status: 101, outcome: 'closed_by_revocation' } },
    { name: 'denied', value: { status: 403, outcome: 'denied' } },
    { name: 'not-dispatched', value: { status: 502, outcome: 'not_dispatched' } },
    { name: 'auth-error', value: { status: 502, outcome: 'upstream_auth' } },
  ];
  const records = variants.map(({ name, value }, i): ProxyRequest => ({
    id: `review-${i}`,
    seq: 100 - i,
    started: started - i * 1000,
    ended: started,
    caller_id: 'arch',
    caller_name: 'arch',
    route: 'claude',
    revision: 1,
    method: 'POST',
    path: '/responses',
    local_prefix: '/route/',
    peer: '127.0.0.1',
    status: 200,
    outcome: 'complete',
    sent: 3,
    received: 4,
    header_ms: 1,
    first_byte_ms: 2,
    total_ms: 100,
    upstream_id: '',
    usage: {
      protocol: 'responses',
      usage: {
        source: 'reported',
        model: `case-${name}`,
        input: 100,
        output: 7000,
        reasoning: i === 0 ? 5100 : 0,
      },
    },
    ...value,
  }));
  for (const [i, model] of ['model-a', 'model-b', 'model-b'].entries())
    records.push({
      ...records[0],
      id: `error-${i}`,
      seq: 50 - i,
      started: started - (20 + i) * 1000,
      status: 400,
      outcome: 'http_error',
      usage: undefined,
      money: { billing: 'pay_as_you_go', kind: 'unmeasured' },
      request_meta: { state: 'observed', model },
      response_meta: undefined,
    });
  return records;
}

test.beforeEach(async ({ page }) => {
  const requests = rows();
  await page.route('http://proxy.test/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/_proxy/assets/proxy.js')
      return route.fulfill({ contentType: 'text/javascript', body: javascript });
    if (path === '/_proxy/assets/proxy.css')
      return route.fulfill({ contentType: 'text/css; charset=utf-8', body: stylesheet });
    if (path === '/_proxy/api/auth/status')
      return route.fulfill({ json: { auth: 'passkey', origin: 'http://proxy.test' } });
    if (path === '/_proxy/api/state') return route.fulfill({ json: snapshot });
    if (path === '/_proxy/api/requests')
      return route.fulfill({
        json: { requests, total: requests.length, page: 1, page_size: 25, next: 0 },
      });
    if (path.startsWith('/_proxy/api/debug/captures/'))
      return route.fulfill({ status: 404, json: { error: 'Capture not found' } });
    if (path === '/_proxy/')
      return route.fulfill({
        contentType: 'text/html',
        body: '<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/_proxy/assets/proxy.css"></head><body><div id="proxy-app"></div><script type="module" src="/_proxy/assets/proxy.js"></script></body></html>',
      });
    return route.abort();
  });
});

for (const width of [1440, 1024, 833, 832, 641, 640, 390, 320]) {
  test(`review counterexamples keep visible cells separate at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto('http://proxy.test/_proxy/?tab=requests');
    const ledger = page.locator('.request-row');
    await expect(ledger).toHaveCount(rows().length - 1); // only model-b repeats
    await expect(page.getByRole('button', { name: 'Expand 2 repeated requests' })).toHaveCount(1);
    const caseRow = (name: string) =>
      ledger.filter({
        has: page.locator('.request-model-label').filter({ hasText: new RegExp(`^case-${name}$`) }),
      });
    for (const [name, summary] of [
      ['limited-turn', 'Output limit'],
      ['failed-turn', 'failed'],
      ['cancelled-turn', 'cancelled'],
      ['denied', 'Denied'],
      ['not-dispatched', 'Not dispatched'],
      ['auth-error', 'Auth error'],
    ])
      await expect(caseRow(name).locator('.request-summary')).toHaveText(summary);
    const statusSelector =
      width > 1024
        ? ':scope > .request-status'
        : width > 640
          ? '.request-stamp-status'
          : '.request-compact-status';
    for (const [name, text] of [
      ['warning', '200 Interrupted'],
      ['unknown', '— Unknown'],
      ['complete-turn', 'Turn complete'],
      ['unknown-turn', 'Outcome unknown'],
      ['closed-connection', '101 Closed by client'],
      ['shutdown-connection', '101 Closed on shutdown'],
    ])
      await expect(caseRow(name).locator(statusSelector)).toHaveText(text);
    if (width > 640) {
      // Inspect actual text ranges, not merely page overflow or grid-cell boxes:
      // a nowrap label can paint into the next column while both boxes fit.
      const violations = await ledger.evaluateAll(
        (nodes, wide) =>
          nodes.flatMap((row) => {
            const status = row.querySelector<HTMLElement>(
              wide ? ':scope > .request-status' : '.request-stamp-status',
            )!;
            const model = row.querySelector<HTMLElement>('.request-model')!.getBoundingClientRect();
            const bounds = status.getBoundingClientRect();
            const walker = document.createTreeWalker(status, NodeFilter.SHOW_TEXT);
            const errors: string[] = [];
            let node: Node | null;
            while ((node = walker.nextNode())) {
              if (!node.textContent?.trim()) continue;
              const range = document.createRange();
              range.selectNodeContents(node);
              for (const rect of range.getClientRects()) {
                if (
                  rect.right > model.left - 0.5 ||
                  rect.left < bounds.left - 0.5 ||
                  rect.right > bounds.right + 0.5
                )
                  errors.push(
                    `${status.textContent}: ${rect.left}..${rect.right} exceeds ${bounds.left}..${bounds.right}, model ${model.left}`,
                  );
              }
            }
            return errors;
          }),
        width > 1024,
      );
      expect(violations).toEqual([]);
    }
    const normal = caseRow('normal');
    if (width > 640 && width <= 832) {
      const client = normal.locator(':scope > .request-client');
      const parts = await client.locator('span').evaluateAll((nodes) =>
        nodes.map((node) => {
          const r = node.getBoundingClientRect();
          return { y: r.y, right: r.right, left: r.left };
        }),
      );
      expect(Math.abs(parts[0].y - parts[1].y)).toBeLessThan(1);
      expect(parts[1].left).toBeGreaterThanOrEqual(parts[0].right);
      expect((await client.boundingBox())!.height).toBeLessThanOrEqual(18);
      expect((await normal.boundingBox())!.height).toBeLessThanOrEqual(60);
    }
    const thinking = normal.locator(
      width <= 640 ? '.request-compact-thinking' : '.request-thinking',
    );
    await expect(thinking).toHaveText('5.1k think');
    await expect(
      caseRow('warning').locator('.request-thinking, .request-compact-thinking'),
    ).toHaveCount(0);
    const grouped = ledger.filter({ has: page.locator('.request-open', { hasText: 'model-b' }) });
    for (const selector of width > 1024
      ? ['.request-time', '.request-duration']
      : width > 640
        ? ['.request-stamp-time', '.request-duration']
        : ['.request-compact-time', '.request-compact-duration']) {
      await expect(grouped.locator(selector)).toHaveAttribute(
        'title',
        /Latest request, not aggregate/,
      );
      await expect(grouped.locator(selector)).toHaveAttribute(
        'aria-label',
        /Latest request, not aggregate/,
      );
    }
    await normal.locator('.request-open').click();
    const detail = normal.locator('.request-detail');
    const requested = detail.locator('.request-insights-grid > div').first().locator('dd').first();
    const response = detail.locator('.request-insights-grid > div').nth(1).locator('dd').first();
    const metadata = detail.locator('.request-meta dd').first();
    expect(
      Math.abs((await requested.boundingBox())!.x - (await metadata.boundingBox())!.x),
    ).toBeLessThan(1);
    if (width <= 640)
      expect(
        Math.abs((await requested.boundingBox())!.x - (await response.boundingBox())!.x),
      ).toBeLessThan(1);
    if (width <= 448) expect((await requested.boundingBox())!.width).toBeGreaterThanOrEqual(110);
    await expect(detail.locator('details')).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: testInfo.outputPath(`review-insights-${width}.png`),
      fullPage: true,
    });
    await page.keyboard.press('Escape');
    await caseRow('limited-turn').locator('.request-open').click();
    await expect(caseRow('limited-turn').locator('.request-meta')).toContainText(
      'Capture scopeWebSocket connection',
    );
    await expect(caseRow('limited-turn').locator('.request-meta')).toContainText(
      'Price reasonExact rate unavailable.',
    );
  });
}
