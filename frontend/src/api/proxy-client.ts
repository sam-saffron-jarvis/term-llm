import type { ChartRange, ChartGroup, MoneyTimeline } from './proxy-timeline';
import type {
  SerializedPublicKeyCredential,
  WebAuthnCreationWireOptions,
  WebAuthnRequestWireOptions,
} from '../hub/domain/types';

export interface ProxyAuthStatus {
  auth: 'passkey' | 'token';
  setup_required: boolean;
  bootstrap_available: boolean;
  recovery_available: boolean;
  bootstrap_verified: boolean;
  recovery_verified: boolean;
  csrf: string;
  origin: string;
}
export interface ProxyCredential {
  id: string;
  name: string;
  created_at: string;
  last_used_at?: string;
}
export type Enrollment = 'bootstrap' | 'recovery' | 'enroll';
export type Assertion = 'login' | 'reauth';
export type ProxyBilling = 'pay_as_you_go' | 'plan' | 'local' | 'unspecified';
export interface ProxyRoute {
  billing?: ProxyBilling;
  account?: string;
  websocket?: boolean;
  allow?: string[];
  redirects?: 'passthrough' | 'refuse';
  usage?: 'off' | 'auto';
  name: string;
  local_prefix: string;
  upstream: string;
  enabled: boolean;
  allow_insecure_http: boolean;
  caller_auth: { header: string; prefix: string };
  set_headers: Record<string, { secret?: string; prefix?: string; value?: string }>;
  request_id_header?: string;
}
export interface ProxySecret {
  name: string;
  origin: string;
  source: 'stored' | 'file';
}
export interface ProxyCaller {
  id: string;
  name: string;
  created: string | number;
  last_used?: number;
  revoked: boolean;
  grants: string[];
}
export interface ProxyPreset {
  id: string;
  label: string;
  upstream: string;
  auth: ProxyRoute['caller_auth'];
  note?: string;
  billing?: ProxyBilling;
  usage?: 'auto';
  websocket?: boolean;
  /** A trusted account of this kind replaces the static key substitution. */
  account_kind?: ProxyAccount['kind'];
}
export interface ProxyAccount {
  name: string;
  kind: 'chatgpt' | 'grok' | 'copilot' | 'claude-code' | 'aws-sigv4';
  state: string;
  generation: number;
  auth_mode?: 'token_only' | 'rotating';
  expires_at?: number;
  updated: number;
}
export interface ProxyAccountInput {
  name: string;
  kind: ProxyAccount['kind'];
  region?: string;
  replace?: boolean;
  credentials: {
    mode?: 'token_only' | 'rotating';
    access_token?: string;
    refresh_token?: string;
    github_token?: string;
    account_id?: string;
    expires_at?: number;
    access_key_id?: string;
    secret_access_key?: string;
    session_token?: string;
    scopes?: string[];
  };
}
export interface ProxyState {
  accounts?: ProxyAccount[];
  money?: ProxyMoneySettings;
  presets?: ProxyPreset[];
  auth: 'passkey' | 'token';
  mode: 'ui' | 'file';
  revision: number;
  routes: ProxyRoute[];
  secrets: ProxySecret[];
  callers: ProxyCaller[];
  csrf: string;
  retention_days?: number;
}
export interface ProxyMoneyRate {
  provider: string;
  model: string;
  tier: string;
  currency: string;
  source: string;
  asof: string;
  input_micros_per_million: number;
  output_micros_per_million: number;
  cache_read_micros_per_million?: number;
  cache_write_micros_per_million?: number;
}
export interface ProxyMoney {
  rate?: ProxyMoneyRate;
  billing: 'pay_as_you_go' | 'plan' | 'local' | 'unspecified';
  kind:
    | 'charged'
    | 'estimated'
    | 'estimated_floor'
    | 'unpriced'
    | 'unmeasured'
    | 'included'
    | 'none'
    | 'no_charge_expected'
    | 'unspecified';
  amount_micros?: number;
  currency?: string;
  source?: string;
  asof?: string;
  reason?: string;
}
export interface MoneyTotal {
  currency: string;
  charged_micros: number;
  estimated_micros: number;
  floor_micros: number;
  total_micros: number;
  qualifier: 'charged' | 'about' | 'at_least';
  sources: { source: string; asof: string; kind: string }[];
}
export interface MoneyPeriod {
  plan_unmeasured: number;
  totals: MoneyTotal[];
  requests: number;
  included: number;
  local: number;
  unpriced: number;
  unmeasured: number;
  unspecified: number;
  no_charge_expected: number;
}
export interface MoneyBreakdown {
  name: string;
  paused: boolean;
  period: MoneyPeriod;
}
export interface MoneySummary {
  asof: string;
  timezone: string;
  month_label: string;
  month: MoneyPeriod;
  today: MoneyPeriod;
  budget?: {
    currency: string;
    limit_micros: number;
    remaining_micros: number;
    used_percent: number;
    warning_only: boolean;
    incomplete: boolean;
  };
  devices: MoneyBreakdown[];
  apps: MoneyBreakdown[];
  providers: MoneyBreakdown[];
}
export interface ProxyMoneySettings {
  timezone: string;
  budget?: { currency: string; limit_micros: number };
  rates?: ProxyMoneyRate[];
}
export interface ProxyRequest {
  money?: ProxyMoney;
  id: string;
  seq: number;
  started: string | number;
  ended: string | number;
  caller_id: string;
  caller_name: string;
  route: string;
  revision: number;
  method: string;
  local_prefix?: string;
  path: string;
  peer: string;
  status: number;
  outcome: string;
  sent: number;
  received: number;
  header_ms: number;
  first_byte_ms: number;
  total_ms: number;
  upstream_id: string;
}
export interface RequestPage {
  requests: ProxyRequest[];
  next: number;
  total?: number;
  page?: number;
  page_size?: number;
}
export type RequestFilters = Partial<
  Record<
    | 'before'
    | 'page'
    | 'page_size'
    | 'caller'
    | 'route'
    | 'method'
    | 'path'
    | 'outcome'
    | 'since'
    | 'until'
    | 'id',
    string
  >
>;

export class ProxyAPIError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ProxyAPIError';
  }
}

// Only these status codes carry the backend's curated, credential-safe JSON errors.
// Bound both the read and display; never display HTML, arbitrary text or other statuses.
async function curatedError(response: Response): Promise<string | undefined> {
  if (
    ![400, 409].includes(response.status) ||
    !/^application\/json(?:\s*;|$)/i.test(response.headers.get('Content-Type') ?? '') ||
    !response.body
  )
    return;
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let body = '';
  let bytes = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > 4096) {
        await reader.cancel();
        return;
      }
      body += decoder.decode(value, { stream: true });
    }
    const parsed = JSON.parse(body + decoder.decode());
    if (typeof parsed?.error === 'string' && parsed.error.trim()) return parsed.error.slice(0, 512);
  } catch {
    /* Malformed or interrupted errors fall back to the generic status message. */
  } finally {
    reader.releaseLock();
  }
}

// All proxy transport lives here. No token is persisted or put in a URL.
export class ProxyClient {
  private csrf = '';
  constructor(private readonly fetcher: typeof fetch = globalThis.fetch.bind(globalThis)) {}

  private async request<T>(path: string, method = 'GET', body?: unknown, text = false): Promise<T> {
    const response = await this.fetcher(`/_proxy/api/${path}`, {
      method,
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      headers: {
        Accept: text && path === 'export' ? 'text/yaml' : 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(method === 'GET' ? {} : { 'X-CSRF-Token': this.csrf }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      if (response.status === 401) this.csrf = '';
      // Never echo arbitrary response bodies: a failed write may contain a credential.
      const messages: Record<number, string> = {
        400: 'Invalid input. Check the fields and try again.',
        401: 'Please sign in to proxy administration.',
        429: 'Too many attempts. Wait before trying again.',
        403: 'Not authorized. Refresh your session and try again.',
        409: 'Conflict: configuration changed, is file-managed, or this secret is still referenced. Refresh state before retrying.',
      };
      throw new ProxyAPIError(
        response.status,
        (await curatedError(response)) ??
          messages[response.status] ??
          `Proxy request failed (${response.status}).`,
      );
    }
    if (text) return (await response.text()) as T;
    if (response.status === 204) return undefined as T;
    const raw = await response.text();
    return (raw ? JSON.parse(raw) : undefined) as T;
  }
  async authStatus(): Promise<ProxyAuthStatus> {
    const status = await this.request<ProxyAuthStatus>('auth/status');
    this.csrf = status.csrf;
    return status;
  }
  verifySecret(kind: 'bootstrap' | 'recovery', token: string): Promise<void> {
    return this.request(`auth/${kind}/verify`, 'POST', { token });
  }
  beginEnrollment(kind: Enrollment, name: string): Promise<WebAuthnCreationWireOptions> {
    return this.request(`auth/${kind}/begin`, 'POST', { name });
  }
  beginAssertion(kind: Assertion): Promise<WebAuthnRequestWireOptions> {
    return this.request(`auth/${kind}/begin`, 'POST', {});
  }
  async finishPasskey(
    kind: Enrollment | Assertion,
    credential: SerializedPublicKeyCredential,
  ): Promise<void> {
    const result = await this.request<{ csrf: string }>(`auth/${kind}/finish`, 'POST', credential);
    this.csrf = result.csrf;
  }
  credentials(): Promise<{ credentials: ProxyCredential[]; sessions: number }> {
    return this.request('auth/credentials');
  }
  deleteCredential(id: string): Promise<void> {
    return this.request('auth/credentials', 'DELETE', { id });
  }
  revokeSessions(): Promise<void> {
    return this.request('auth/sessions/revoke', 'POST', {});
  }
  async state(): Promise<ProxyState> {
    const state = await this.request<ProxyState>('state');
    this.csrf = state.csrf;
    // Go's empty slices/maps may be serialized as null on an empty installation.
    return {
      ...state,
      routes: (state.routes ?? []).map((route) => ({
        ...route,
        set_headers: route.set_headers ?? {},
      })),
      secrets: state.secrets ?? [],
      callers: (state.callers ?? []).map((caller) => ({ ...caller, grants: caller.grants ?? [] })),
    };
  }
  async login(token: string): Promise<void> {
    const result = await this.request<{ csrf: string }>('login', 'POST', { token });
    this.csrf = result.csrf;
  }
  async logout(): Promise<void> {
    await this.request('logout', 'POST');
    this.csrf = '';
  }
  config(revision: number, routes: ProxyRoute[]): Promise<void> {
    return this.request('config', 'PUT', { revision, routes });
  }
  reload(): Promise<void> {
    return this.request('reload', 'POST');
  }
  secret(name: string, origin: string, value: string, replace = false): Promise<void> {
    return this.request('secrets', 'PUT', { name, origin, value, replace });
  }
  deleteSecret(name: string): Promise<void> {
    return this.request('secrets', 'DELETE', { name });
  }
  createCaller(
    name: string,
    grants: string[],
    revision: number,
  ): Promise<{ token: string; caller: ProxyCaller }> {
    return this.request('callers', 'POST', { name, grants, revision });
  }
  updateCaller(id: string, grants: string[], revision: number): Promise<void> {
    return this.request('callers', 'PUT', { id, grants, revision });
  }
  revokeCaller(id: string): Promise<void> {
    return this.request('callers', 'DELETE', { id });
  }
  async accounts(): Promise<ProxyAccount[]> {
    const result = await this.request<{ accounts: ProxyAccount[] | null }>('accounts');
    return result.accounts ?? [];
  }
  saveAccount(input: ProxyAccountInput): Promise<{ account: ProxyAccount }> {
    return this.request('accounts', input.replace ? 'PUT' : 'POST', input);
  }
  signOutAccount(name: string): Promise<{ account: ProxyAccount }> {
    return this.request('accounts', 'DELETE', { name });
  }
  timeline(range: ChartRange = '30d', group: ChartGroup = 'total'): Promise<MoneyTimeline> {
    return this.request(`timeline?range=${range}&group=${group}`);
  }
  summary(): Promise<MoneySummary> {
    return this.request('summary');
  }
  moneySettings(): Promise<ProxyMoneySettings> {
    return this.request('money');
  }
  money(settings: ProxyMoneySettings): Promise<void> {
    return this.request('money', 'PUT', settings);
  }
  requests(filters: RequestFilters): Promise<RequestPage> {
    return this.request(`requests?${this.query(filters)}`);
  }
  requestExport(filters: RequestFilters): Promise<string> {
    return this.request(`requests/export?${this.query(filters)}`, 'GET', undefined, true);
  }
  importYAML(revision: number, yaml: string): Promise<void> {
    return this.request('import', 'POST', { revision, yaml });
  }
  exportYAML(): Promise<string> {
    return this.request('export', 'GET', undefined, true);
  }
  private query(filters: RequestFilters): string {
    return new URLSearchParams(
      Object.entries(filters).filter(([, value]) => value !== ''),
    ).toString();
  }
}
