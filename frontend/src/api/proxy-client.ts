import type { ChartRange, ChartGroup, MoneyTimeline, TimelineScope } from './proxy-timeline';
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
/** Local client credential location, or the matching upstream credential shape. */
export interface ProxyAuth {
  header: string;
  prefix: string;
}
export interface ProxyHeaderRule {
  secret?: string;
  prefix?: string;
  value?: string;
}
/**
 * One subpath of a unified route. The backend selects a rule by path prefix;
 * the rule's caller_auth is the client credential location for that subpath.
 * Parent upstream/set_headers are unused once rules exist.
 */
export interface ProxyRouteRule {
  path_prefix: string;
  upstream: string;
  caller_auth: ProxyAuth;
  set_headers: Record<string, ProxyHeaderRule>;
  allow?: string[];
}
export interface ProxyRoute {
  provider: string;
  profile?: string;
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
  caller_auth: ProxyAuth;
  set_headers: Record<string, ProxyHeaderRule>;
  request_id_header?: string;
  rules?: ProxyRouteRule[];
}
export interface ProxyAdminRoute extends ProxyRoute {
  profile: string;
  serves: string[];
}
/** Creation-time defaults for a unified route's subpath rules. */
export interface ProxyPresetRule {
  path_prefix: string;
  upstream: string;
  auth: ProxyAuth;
  allow?: string[];
}
export type ProxyDeviceProvider = 'chatgpt' | 'grok';
export interface ProxyDeviceSignIn {
  id: string;
  verification_url: string;
  user_code: string;
}
export type ProxyDeviceSetupStatus =
  | { status: 'pending' }
  | { status: 'connected'; route?: string }
  | { status: 'failed'; error?: string };
export type ProxyNativeLauncher = 'claude' | 'agy';
export interface ProxyNativeConnection {
  revision: number;
  url: string;
  /** Omitted when shared sign-in armed an existing client, which keeps its token. */
  token?: string;
  caller_id: string;
}
/** Shared sign-in arms an existing client or creates one with this name. */
export type ProxyNativeSignInClient = { caller: string } | { name: string };
export interface ProxySecret {
  name: string;
  source: 'stored' | 'file';
  routes: string[] | null;
}
export interface ProxyCaller {
  id: string;
  name: string;
  created: string | number;
  last_used?: number;
  revoked: boolean;
  grants: string[];
  created_by: string;
  external_id: string;
}
export interface ProxyAdminKey {
  id: string;
  name: string;
  created: number;
  last_used: number;
}
export interface ProxyPreset {
  id: string;
  label: string;
  upstream: string;
  auth: ProxyAuth;
  note?: string;
  billing?: ProxyBilling;
  usage?: 'off' | 'auto';
  websocket?: boolean;
  /** A trusted account of this kind replaces the static key substitution. */
  account_kind?: ProxyAccount['kind'];
  /** Subpath rules that make this one route serve several protocol roots. */
  rules?: ProxyPresetRule[];
  family: ProxyProviderFamily;
  allow?: string[];
  /** Sibling presets created alongside this one under their own fixed names. */
  companions?: string[];
}
export interface ProxyAccount {
  name: string;
  kind: 'chatgpt' | 'grok' | 'copilot' | 'claude-bin' | 'agy-bin' | 'aws-sigv4';
  state: string;
  generation: number;
  device_reconnect?: 'eligible' | 'missing_identity' | 'custom_settings';
  auth_mode?: 'token_only' | 'rotating' | 'vault' | 'shared_vault';
  expires_at?: number;
  updated: number;
}
export interface ProxyAccountInput {
  name: string;
  kind: ProxyAccount['kind'];
  region?: string;
  replace?: boolean;
  credentials: {
    mode?: 'token_only' | 'rotating' | 'vault';
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
export type ProxyProviderClientModel =
  | string
  | {
      id?: string;
      alias?: string;
      context_window?: number;
      max_output_tokens?: number;
      parse_reasoning?: boolean;
      include_reasoning?: boolean;
      thinking_param?: string;
      reasoning_efforts?: string[];
      default_reasoning_effort?: string;
      vision_via?: string;
    };

export interface ProxyProviderClientConfig {
  type?: string;
  model?: string;
  fast_model?: string;
  fast_provider?: string;
  service_tier?: string;
  allowed_models?: string[];
  models?: ProxyProviderClientModel[];
  reasoning?: string;
  vision_via?: string;
  context_window?: number;
  max_output_tokens?: number;
  vllm_thinking_param?: string;
  thinking_param?: string;
  parse_reasoning?: boolean;
  include_reasoning?: boolean;
  no_stream_options?: boolean;
  use_websocket?: boolean;
  region?: string;
}

export interface ProxyParsedProviderConfig {
  name?: string;
  upstream?: string;
  config: ProxyProviderClientConfig;
  config_yaml: string;
  stripped?: string[];
}

export type ProxyProviderFamily =
  'inference' | 'classification' | 'search' | 'media' | 'embeddings';
export type ProxyProviderStatus = 'green' | 'orange' | 'red' | 'grey';
export type ProxyDefaultSlot =
  'search' | 'fetch' | 'image' | 'audio' | 'music' | 'transcription' | 'classify';
export type ProxyDefaults = Partial<Record<ProxyDefaultSlot, string>>;

export interface ProxyStoredProvider {
  label: string;
  family?: ProxyProviderFamily;
  client?: ProxyProviderClientConfig;
}

export interface ProxyProviderCreate {
  name: string;
  label?: string;
  preset: string;
  routes?: (Pick<ProxyRoute, 'name' | 'profile'> &
    Partial<
      Pick<ProxyRoute, 'local_prefix' | 'upstream' | 'account' | 'allow_insecure_http' | 'enabled'>
    > & { profile: string })[];
  secret?: string;
  value?: string;
}

export interface ProxyProvider {
  id: string;
  label: string;
  family: ProxyProviderFamily;
  status: ProxyProviderStatus;
  reason: string;
  routes: string[];
  config?: ProxyProviderClientConfig;
  config_yaml?: string;
}

export interface ProxyState {
  accounts?: ProxyAccount[];
  providers: ProxyProvider[];
  defaults: ProxyDefaults | null;
  money?: ProxyMoneySettings;
  presets?: ProxyPreset[];
  auth: 'passkey' | 'token';
  mode: 'ui' | 'file';
  revision: number;
  routes: ProxyAdminRoute[];
  secrets: ProxySecret[];
  callers: ProxyCaller[];
  admin_keys: ProxyAdminKey[];
  csrf: string;
  retention_days?: number;
  has_requests?: boolean;
  has_native_usage?: boolean;
}
export interface ProxyUsageWindow {
  label: string;
  duration_minutes: number;
  used_percent: number;
  resets_at?: string;
  percent_unknown?: boolean;
  limit_reached?: boolean;
  detail?: string;
  disabled?: boolean;
}
export interface ProxyUsageGroup {
  name: string;
  detail?: string;
  windows: ProxyUsageWindow[];
}
export interface ProxyUsageReport {
  plan?: string;
  detail?: string;
  groups: ProxyUsageGroup[];
}
export interface ProxyUsageProvider {
  id: string;
  route?: string;
  report?: ProxyUsageReport;
  updated_at?: string;
  error?: string;
}
export interface ProxyUsageSnapshot {
  interval_minutes: number;
  refreshing: boolean;
  providers: ProxyUsageProvider[];
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
export interface ProxyAPIEquivalent {
  kind: 'estimated' | 'estimated_floor' | 'unpriced' | 'unmeasured';
  amount_micros?: number;
  currency?: string;
  source?: string;
  asof?: string;
  reason?: string;
  rate?: ProxyMoneyRate;
}
export interface ProxyMoney {
  api_equivalent?: ProxyAPIEquivalent;
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
  api_equivalent_totals?: MoneyTotal[];
  api_equivalent_covered?: number;
  api_equivalent_unpriced?: number;
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
// Response-only evidence. Missing counters and -1 are unknown, not zero;
// protocol preserves whether cache/reasoning counts overlap other counters.
export interface ProxyObservedUsage {
  protocol?:
    | 'responses'
    | 'chat_completions'
    | 'messages'
    | 'gemini'
    | 'ollama'
    | 'bedrock'
    | 'not_applicable'
    | '';
  usage?: {
    input?: number;
    cached_input?: number;
    cache_write?: number;
    output?: number;
    reasoning?: number;
    total?: number;
    model?: string;
    tier?: string;
    ambiguous?: boolean;
    source?: 'reported' | 'partial' | 'unknown' | 'not_applicable';
    turns?: number;
  };
}
/** Passive HTTP settings, never billing inputs. Missing legacy metadata is unobserved. */
export interface ProxyRequestMetadata {
  state: 'observed' | 'not_observed' | 'unsupported' | 'truncated' | 'invalid';
  protocol?: string;
  model?: string;
  reasoning_effort?: string;
  reasoning_mode?: string;
  reasoning_context?: string;
  reasoning_summary?: string;
  thinking_mode?: string;
  thinking_budget?: number;
  thinking_budget_mode?: 'dynamic';
  output_limit?: number;
  output_limit_mode?: 'unlimited' | 'context';
  service_tier?: string;
  temperature?: number;
  top_p?: number;
  stream?: boolean;
  verbosity?: string;
  tool_choice?: string;
  parallel_tools?: boolean;
  declared_tools?: number;
  multi_agent?: boolean;
  agent_limit?: number;
  cache_mode?: string;
  cache_ttl?: string;
}
/** Finite provider diagnostics, separate from usage/price and HTTP outcome. */
export interface ProxyResponseMetadata {
  state: 'observed' | 'partial';
  status?: string;
  finish?: string;
  incomplete_reason?: string;
  choices?: { index: number; reason: string }[];
  cache_write_5m?: number;
  cache_write_1h?: number;
  web_search_requests?: number;
  web_fetch_requests?: number;
}
export interface ProxyRequest {
  request_meta?: ProxyRequestMetadata;
  response_meta?: ProxyResponseMetadata;
  usage?: ProxyObservedUsage;
  money?: ProxyMoney;
  id: string;
  // Responses WebSocket turn rows link to the original connection request and capture.
  websocket_connection_id?: string;
  seq: number;
  started: string | number;
  ended: string | number;
  caller_id: string;
  caller_name: string;
  tag?: string;
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

export interface ProxyDebugState {
  enabled: boolean;
  directory: string;
  preview_limit_bytes?: number;
  max_bytes_per_stream?: number;
  max_total_bytes?: number;
  remaining_bytes?: number;
  record_count?: number;
  record_limit?: number;
}
export interface ProxyDebugCaptureFiles {
  id: string;
  finished: boolean;
  files: ProxyDebugFile[];
}
export interface ProxyDebugCapture extends ProxyDebugCaptureFiles {
  started: number;
  ended?: number;
  route: string;
  method: string;
  local_path: string;
  outcome: string;
  status?: number;
  websocket: boolean;
}
export type ProxyDebugFile =
  | 'manifest.json'
  | 'request.body'
  | 'response.body'
  | 'request.headers'
  | 'upstream-request.headers'
  | 'upstream-response.headers'
  | 'websocket-client.frames'
  | 'websocket-server.frames';
export interface ProxyDebugPreview {
  id: string;
  file: ProxyDebugFile;
  bytes_returned: number;
  content: string;
  encoding: 'utf8' | 'base64' | 'websocket';
  offset: number;
  /** The next frame-aligned offset for a decoded preview, omitted at the end. */
  next_offset?: number;
  total_bytes: number;
  truncated: boolean;
}

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

  private async request<T>(
    path: string,
    method = 'GET',
    body?: unknown,
    text = false,
    clearSession = true,
    signal?: AbortSignal,
  ): Promise<T> {
    const response = await this.fetcher(`/_proxy/api/${path}`, {
      method,
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      ...(signal ? { signal } : {}),
      headers: {
        Accept: text && path === 'export' ? 'text/yaml' : 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(method === 'GET' ? {} : { 'X-CSRF-Token': this.csrf }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      if (response.status === 401 && clearSession) this.csrf = '';
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
        serves: route.serves ?? [],
        set_headers: route.set_headers ?? {},
        ...(route.rules
          ? {
              rules: route.rules.map((rule) => ({
                ...rule,
                caller_auth: rule.caller_auth ?? { header: '', prefix: '' },
                set_headers: rule.set_headers ?? {},
              })),
            }
          : {}),
      })),
      secrets: state.secrets ?? [],
      callers: (state.callers ?? []).map((caller) => ({ ...caller, grants: caller.grants ?? [] })),
      admin_keys: state.admin_keys ?? [],
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
  startDeviceSetup(
    provider: ProxyDeviceProvider,
    input: { name?: string; account?: string },
  ): Promise<ProxyDeviceSignIn> {
    return this.request(`${provider}/setup`, 'POST', input);
  }
  deviceSetupStatus(provider: ProxyDeviceProvider, id: string): Promise<ProxyDeviceSetupStatus> {
    return this.request(`${provider}/setup?id=${encodeURIComponent(id)}`);
  }
  cancelDeviceSetup(provider: ProxyDeviceProvider, id: string): Promise<void> {
    return this.request(`${provider}/setup?id=${encodeURIComponent(id)}`, 'DELETE');
  }
  // Operator launchers create or reconnect a shared route; worker launchers require an existing route.
  setupNative(
    ...args:
      | [launcher: ProxyNativeLauncher, revision: number, route: string]
      | [
          launcher: ProxyNativeLauncher,
          revision: number,
          route: undefined,
          sharedAction: 'create',
          client: ProxyNativeSignInClient,
        ]
      | [
          launcher: ProxyNativeLauncher,
          revision: number,
          route: string,
          sharedAction: 'connect',
          client: ProxyNativeSignInClient,
        ]
  ): Promise<ProxyNativeConnection> {
    const [launcher, revision, route, sharedAction, client] = args;
    return this.request(`${launcher}/setup`, 'POST', {
      revision,
      ...(route ? { route } : {}),
      ...(sharedAction ? { shared_action: sharedAction } : {}),
      ...client,
    });
  }
  clearNative(launcher: ProxyNativeLauncher, revision: number): Promise<void> {
    return this.request(`${launcher}/setup`, 'DELETE', { revision });
  }
  config(
    state: ProxyState,
    routes: ProxyRoute[],
    newProviders: Record<string, ProxyStoredProvider> = {},
    providerConfigYAML?: Record<string, string>,
  ): Promise<void> {
    const providers = Object.fromEntries(
      state.providers.map((provider) => [
        provider.id,
        {
          label: provider.label,
          ...(provider.config ? { client: provider.config } : {}),
          // Admin state exposes the effective family; profiled routes must omit it in storage.
          ...(provider.family !== 'inference' &&
          !routes.some((r) => r.provider === provider.id && r.profile)
            ? { family: provider.family }
            : {}),
        },
      ]),
    );
    return this.request('config', 'PUT', {
      revision: state.revision,
      routes: routes.map((route) => {
        const config: ProxyRoute & Partial<ProxyAdminRoute> = { ...route };
        delete config.serves;
        return config;
      }),
      providers: { ...providers, ...newProviders },
      defaults: state.defaults,
      ...(providerConfigYAML ? { provider_config_yaml: providerConfigYAML } : {}),
    });
  }
  createProvider(revision: number, input: ProxyProviderCreate): Promise<{ revision: number }> {
    return this.request('providers', 'POST', { revision, ...input });
  }
  renameProviderLabel(
    revision: number,
    name: string,
    label: string,
  ): Promise<{ revision: number }> {
    return this.request(`providers/${encodeURIComponent(name)}`, 'PUT', { revision, label });
  }
  deleteProvider(revision: number, name: string): Promise<{ revision: number }> {
    return this.request(`providers/${encodeURIComponent(name)}`, 'DELETE', { revision });
  }
  updateDefaults(revision: number, defaults: ProxyDefaults): Promise<{ revision: number }> {
    return this.request('defaults', 'PUT', { revision, defaults });
  }
  updateProviderConfig(
    revision: number,
    provider: string,
    yaml: string,
  ): Promise<{ revision: number; config?: ProxyProviderClientConfig; config_yaml?: string }> {
    return this.request('provider-config', 'PUT', { revision, provider, yaml });
  }
  parseProviderConfig(yaml: string): Promise<ProxyParsedProviderConfig> {
    return this.request('provider-config/parse', 'POST', { yaml });
  }
  debug(): Promise<ProxyDebugState> {
    return this.request('debug');
  }
  setDebug(enabled: boolean): Promise<ProxyDebugState> {
    return this.request('debug', 'POST', {
      enabled,
      ...(enabled ? { acknowledge_sensitive: true } : {}),
    });
  }
  debugCaptures(): Promise<{ captures: ProxyDebugCapture[] | null; limit?: number }> {
    return this.request('debug/captures?limit=50');
  }
  // A late 401 from a closed preview must not change the current session.
  // The live view still handles authentication errors through UIContext.run.
  debugCapture(id: string): Promise<ProxyDebugCaptureFiles> {
    return this.request(`debug/captures/${encodeURIComponent(id)}`, 'GET', undefined, false, false);
  }
  debugFile(
    id: string,
    file: ProxyDebugFile,
    offset = 0,
    limit = 65536,
    view?: 'decoded',
  ): Promise<ProxyDebugPreview> {
    const query = `offset=${offset}&limit=${limit}${view ? `&view=${view}` : ''}`;
    return this.request(
      `debug/captures/${encodeURIComponent(id)}/${encodeURIComponent(file)}?${query}`,
      'GET',
      undefined,
      false,
      false,
    );
  }
  reload(): Promise<void> {
    return this.request('reload', 'POST');
  }
  secret(name: string, value: string, replace = false): Promise<void> {
    return this.request('secrets', 'PUT', { name, value, ...(replace ? { replace } : {}) });
  }
  deleteSecret(name: string): Promise<void> {
    return this.request('secrets', 'DELETE', { name });
  }
  createAdminKey(name: string): Promise<{ key: ProxyAdminKey; token: string }> {
    return this.request('admin-keys', 'POST', { name });
  }
  revokeAdminKey(
    id: string,
    revokeClients: boolean,
  ): Promise<{ ok: boolean; revoked_clients: number }> {
    return this.request('admin-keys', 'DELETE', { id, revoke_clients: revokeClients });
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
  createProviderCaller(
    name: string,
    providers: string[],
    revision: number,
  ): Promise<{ token: string; caller: ProxyCaller }> {
    return this.request('callers', 'POST', { name, providers, revision });
  }
  updateProviderApprovals(
    id: string,
    changes: { id: string; approved: boolean }[],
    revision: number,
  ): Promise<void> {
    return this.request('callers', 'PUT', { id, provider_changes: changes, revision });
  }
  revokeCaller(id: string): Promise<void> {
    return this.request('callers', 'DELETE', { id });
  }
  purgeCaller(id: string): Promise<void> {
    return this.request('callers', 'DELETE', { id, purge: true });
  }
  purgeAllRevokedCallers(): Promise<void> {
    return this.request('callers', 'DELETE', { purge_revoked: true });
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
  /** Deletes an account no route uses, freeing its name. */
  removeAccount(name: string): Promise<{ removed: string }> {
    return this.request('accounts', 'DELETE', { name, remove: true });
  }
  timeline(
    range: ChartRange = '30d',
    group: ChartGroup = 'total',
    timezone?: string,
    scope?: TimelineScope,
  ): Promise<MoneyTimeline> {
    const params = new URLSearchParams({ range, group });
    if (timezone) params.set('timezone', timezone);
    for (const group of ['client', 'provider', 'model'] as const) {
      const key = scope?.[group];
      if (key !== undefined) params.set(`scope_${group}`, key);
    }
    return this.request(`timeline?${params.toString()}`);
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
  usage(signal?: AbortSignal): Promise<ProxyUsageSnapshot> {
    return this.request('usage', 'GET', undefined, false, !signal, signal);
  }
  refreshUsage(signal?: AbortSignal): Promise<void> {
    return this.request('usage/refresh', 'POST', undefined, false, !signal, signal);
  }
  setUsageInterval(intervalMinutes: number, signal?: AbortSignal): Promise<void> {
    return this.request(
      'usage/settings',
      'PUT',
      { interval_minutes: intervalMinutes },
      false,
      !signal,
      signal,
    );
  }
  requests(filters: RequestFilters, signal?: AbortSignal): Promise<RequestPage> {
    return this.request(
      `requests?${this.query(filters)}`,
      'GET',
      undefined,
      false,
      !signal,
      signal,
    );
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
