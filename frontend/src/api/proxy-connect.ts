import { ProxyAPIError } from './proxy-client';
export type ClaudeConnectStatus =
  | 'pending'
  | 'approved'
  | 'committing'
  | 'complete'
  | 'acknowledged'
  | 'canceled'
  | 'expired'
  | 'failed';
export interface ClaudeConnectTicket {
  id: string;
  code: string;
  origin: string;
  expires: number; // Unix seconds; never the upstream token's unknown expiry.
  status: ClaudeConnectStatus;
}
export interface ClaudeConnectRequest extends ClaudeConnectTicket {
  action: 'auto' | 'reconnect';
  resolved_action: 'add' | 'device' | 'reconnect' | 'collision';
  source_account?: string; // Authenticated resolution only; never infer from pending.
  account: string;
  client_name: string;
  route: string;
  prefix: string;
}
export interface ClaudeConnectResult {
  token?: string; // Absent for account-only reconnect; otherwise scoped proxy only.
  caller_id?: string;
  gateway_url: string;
  account?: string;
  route?: string;
}
export interface ClaudeConnectReceipt {
  status: ClaudeConnectStatus;
  request?: ClaudeConnectTicket | ClaudeConnectRequest;
  result?: ClaudeConnectResult;
}
export interface ClaudeConnectCapability {
  id: string;
  secret: string;
}
// An optional parent adapter can implement these on ProxyClient. Otherwise
// createClaudeConnectAdmin reuses its current post-reauth state/CSRF safely.
export interface ClaudeConnectAdmin {
  connectRequest(id: string): Promise<ClaudeConnectRequest>;
  connectApprove(id: string, code: string): Promise<ClaudeConnectRequest>;
}
export class ClaudeConnectError extends Error {
  constructor(readonly status = 0) {
    super(
      status === 410
        ? 'This connection request expired.'
        : status === 409
          ? 'This connection request was already used or changed. Check its saved status.'
          : status === 401 || status === 404
            ? 'This connection capability is unavailable or expired.'
            : 'The connection could not be confirmed. Check the same request; no new request was created.',
    );
    this.name = 'ClaudeConnectError';
  }
}
const statuses = new Set([
  'pending',
  'approved',
  'committing',
  'complete',
  'acknowledged',
  'canceled',
  'expired',
  'failed',
]);
const hex = /^[a-f0-9]+$/;
function validCapability(cap: ClaudeConnectCapability) {
  if (
    cap.id.length !== 32 ||
    cap.secret.length !== 64 ||
    !hex.test(cap.id) ||
    !hex.test(cap.secret)
  )
    throw new ClaudeConnectError();
}
export function newClaudeConnectCapability(): ClaudeConnectCapability {
  const random = (bytes: number) =>
    [...globalThis.crypto.getRandomValues(new Uint8Array(bytes))]
      .map((n) => n.toString(16).padStart(2, '0'))
      .join('');
  try {
    return { id: random(16), secret: random(32) };
  } catch {
    throw new ClaudeConnectError();
  }
}
export function parseClaudeConnectTicket(value: unknown): ClaudeConnectTicket {
  if (!value || typeof value !== 'object') throw new ClaudeConnectError();
  const r = value as ClaudeConnectTicket;
  if (
    !/^[a-f0-9]{32}$/.test(r.id) ||
    typeof r.code !== 'string' ||
    !/^[a-f0-9]{8}$/.test(r.code) ||
    typeof r.origin !== 'string' ||
    !Number.isSafeInteger(r.expires) ||
    r.expires <= 0 ||
    !statuses.has(r.status)
  )
    throw new ClaudeConnectError();
  return { id: r.id, code: r.code, origin: r.origin, expires: r.expires, status: r.status };
}
export function parseClaudeConnectRequest(value: unknown): ClaudeConnectRequest {
  const ticket = parseClaudeConnectTicket(value);
  const r = value as ClaudeConnectRequest;
  if (
    !['auto', 'reconnect'].includes(r.action) ||
    !['add', 'device', 'reconnect', 'collision'].includes(r.resolved_action)
  )
    throw new ClaudeConnectError();
  for (const key of ['account', 'client_name', 'route', 'prefix'] as const)
    if (
      typeof r[key] !== 'string' ||
      r[key].length > 512 ||
      (key !== 'client_name' && !r[key] && r.resolved_action !== 'collision')
    )
      throw new ClaudeConnectError();
  if (
    (r.action === 'auto' && r.resolved_action === 'reconnect') ||
    (r.action === 'reconnect' && !['reconnect', 'collision'].includes(r.resolved_action))
  )
    throw new ClaudeConnectError();
  if (
    r.resolved_action !== 'collision' &&
    (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(r.account) ||
      r.route !== r.account ||
      r.prefix !== `/${r.account}/` ||
      (r.action === 'auto' && !r.client_name))
  )
    throw new ClaudeConnectError();
  if (
    r.source_account !== undefined &&
    (typeof r.source_account !== 'string' ||
      !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(r.source_account))
  )
    throw new ClaudeConnectError();
  return {
    ...ticket,
    ...(r.source_account === undefined ? {} : { source_account: r.source_account }),
    action: r.action,
    resolved_action: r.resolved_action,
    account: r.account,
    client_name: r.client_name,
    route: r.route,
    prefix: r.prefix,
  };
}
function parseReceipt(value: unknown): ClaudeConnectReceipt {
  if (!value || typeof value !== 'object') throw new ClaudeConnectError();
  const raw = value as ClaudeConnectReceipt;
  // Wire replies are flat. Normalize only their public binding metadata;
  // unresolved pending replies never acquire a guessed resolved action.
  const ticket = parseClaudeConnectTicket(raw);
  if (['approved', 'complete'].includes(ticket.status) && !('resolved_action' in raw))
    throw new ClaudeConnectError();
  const request = 'resolved_action' in raw ? parseClaudeConnectRequest(raw) : ticket;
  const status = ticket.status;
  let result: ClaudeConnectResult | undefined;
  if (raw.result) {
    const r = raw.result;
    if (
      !['complete', 'acknowledged', 'expired'].includes(status) ||
      (r.token !== undefined &&
        r.token !== '' &&
        (status !== 'complete' ||
          !/^sk-tlp-[a-f0-9]{64}$/.test(r.token) ||
          typeof r.caller_id !== 'string' ||
          !r.caller_id)) ||
      typeof r.gateway_url !== 'string'
    )
      throw new ClaudeConnectError();
    result = {
      ...(r.token ? { token: r.token } : {}),
      ...(typeof r.caller_id === 'string' ? { caller_id: r.caller_id } : {}),
      gateway_url: r.gateway_url,
    };
  }
  return { status, request, result };
}

// Public capability transport is deliberately separate from administrator auth.
// No browser cookies, storage, redirect following or secret URL parameters.
export class ClaudeConnectClient {
  constructor(private readonly fetcher: typeof fetch = globalThis.fetch.bind(globalThis)) {}
  private async request(
    path: string,
    method: string,
    body?: unknown,
    cap?: ClaudeConnectCapability,
    json = true,
    admin = false,
    csrf?: string,
  ): Promise<unknown> {
    if (cap) validCapability(cap);
    try {
      const response = await this.fetcher(`/_proxy/api/connect/${path}`, {
        method,
        credentials: admin ? 'same-origin' : 'omit',
        cache: 'no-store',
        redirect: 'error',
        referrerPolicy: 'no-referrer',
        headers: {
          Accept: 'application/json',
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
          ...(cap ? { Authorization: `Bearer ${cap.secret}` } : {}),
          ...(csrf === undefined ? {} : { 'X-CSRF-Token': csrf }),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!response.ok) {
        if (admin && response.status === 401)
          throw new ProxyAPIError(401, 'Please sign in again to approve this connection.');
        throw new ClaudeConnectError(response.status);
      }
      return json ? await response.json() : undefined;
    } catch (error) {
      if (error instanceof ClaudeConnectError || error instanceof ProxyAPIError) throw error;
      throw new ClaudeConnectError();
    }
  }
  async start(
    cap: ClaudeConnectCapability,
    options: { account: string; client_name: string; action: 'auto' | 'reconnect' },
  ): Promise<ClaudeConnectTicket> {
    validCapability(cap);
    return parseClaudeConnectTicket(
      await this.request('start', 'POST', {
        id: cap.id,
        secret: cap.secret,
        account: options.account,
        client_name: options.client_name,
        action: options.action,
      }),
    );
  }
  async status(cap: ClaudeConnectCapability): Promise<ClaudeConnectReceipt> {
    return parseReceipt(
      await this.request(`status?id=${encodeURIComponent(cap.id)}`, 'GET', undefined, cap),
    );
  }
  async commit(cap: ClaudeConnectCapability, token?: string): Promise<ClaudeConnectReceipt> {
    return parseReceipt(
      await this.request('commit', 'POST', { id: cap.id, ...(token ? { token } : {}) }, cap),
    );
  }
  async ack(cap: ClaudeConnectCapability): Promise<void> {
    await this.request('ack', 'POST', { id: cap.id }, cap, false);
  }
  async cancel(cap: ClaudeConnectCapability): Promise<void> {
    await this.request('cancel', 'POST', { id: cap.id }, cap, false);
  }
  async inspect(id: string): Promise<ClaudeConnectRequest> {
    if (!/^[a-f0-9]{32}$/.test(id)) throw new ClaudeConnectError();
    return parseClaudeConnectRequest(
      await this.request(
        `request?id=${encodeURIComponent(id)}`,
        'GET',
        undefined,
        undefined,
        true,
        true,
      ),
    );
  }
  async approve(id: string, code: string, csrf: string): Promise<ClaudeConnectRequest> {
    if (!/^[a-f0-9]{32}$/.test(id) || !/^[a-f0-9]{8}$/.test(code) || !csrf)
      throw new ClaudeConnectError();
    return parseClaudeConnectRequest(
      await this.request('approve', 'POST', { id, code }, undefined, true, true, csrf),
    );
  }
}

// Existing begin/finish assertion methods remain authoritative. Read current
// session CSRF after reauthentication; never combine cookies with a capability.
export function createClaudeConnectAdmin(
  api: { state(): Promise<{ csrf: string }> },
  client: ClaudeConnectClient,
): ClaudeConnectAdmin {
  return {
    connectRequest: (id) => client.inspect(id),
    connectApprove: async (id, code) => {
      const state = await api.state();
      return client.approve(id, code, state.csrf);
    },
  };
}
