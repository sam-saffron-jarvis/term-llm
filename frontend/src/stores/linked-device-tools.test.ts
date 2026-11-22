import { signal } from '@preact/signals';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AppConfig } from '../app/config';
import type { ClientTool } from '../domain/client-tools';
import { storageKeys } from '../platform/storage';
import type { PageToolHost } from '../platform/webmcp';
import type { AppStoreServices } from './app-store-services';
import { AppStore } from './app-store';
import { WebMCPStore } from './webmcp-store';

const ping: ClientTool = {
  name: 'ping',
  title: 'Ping',
  description: 'Check the phone',
  inputSchema: { type: 'object', properties: {} },
  readOnly: true,
};

const config: AppConfig = {
  prefix: '/ui',
  version: 'v1',
  sidebarCategories: ['all'],
  agentName: '',
  agentNames: ['jarvis'],
  title: '',
  locationSharing: false,
  worktrees: false,
  hub: null,
  vapidKey: '',
};

type TestWindow = Window & { __termLLMDeviceTools?: unknown };

const services = () =>
  ({ storage: localStorage, keys: storageKeys(null) }) as unknown as AppStoreServices;

function host(): PageToolHost {
  return {
    list: vi.fn(async () => [ping]),
    execute: vi.fn(async () => 'pong'),
    subscribe: vi.fn(() => () => undefined),
    provider: () => 'iPhone',
  };
}

/** A store for a page whose linked-device status and grant the test controls. */
async function accessStore(linkedMode: boolean, grant: boolean, pageHost = host()) {
  const allowed = signal(!linkedMode || grant);
  const linked = signal(linkedMode);
  const store = new WebMCPStore(services(), pageHost, { allowed, linked });
  await store.refresh();
  return { store, allowed, linked, pageHost };
}

const call = { callId: 'call_1', name: 'ping', arguments: '{}' };

beforeEach(() => localStorage.clear());

describe('WebMCPStore on a linked device', () => {
  it('offers nothing, pins nothing, and ignores the toggle without an explicit grant', async () => {
    const { store, pageHost } = await accessStore(true, false);
    store.setEnabled('s1', true);
    expect(store.enabledFor('s1')).toBe(false);
    expect(store.definitions('s1')).toEqual([]);
    expect(localStorage.length).toBe(0);
    await expect(store.run(call, new AbortController().signal)).resolves.toMatchObject({
      ok: false,
    });
    expect(pageHost.execute).not.toHaveBeenCalled();
  });

  it('is off until the user turns it on for that conversation', async () => {
    const { store } = await accessStore(true, true);
    // Whatever default the owner left in this browser is not consent here.
    localStorage.setItem(storageKeys(null).webMCPDefault, 'true');
    store.reloadSettings();
    expect(store.enabledFor('s1')).toBe(false);
    expect(store.definitions('s1')).toEqual([]);

    store.setEnabled('s2', true);
    expect(store.definitions('s2').map((tool) => tool.name)).toEqual(['webmcp__ping']);
    expect(store.definitions('s3')).toEqual([]);
  });

  it('does not turn a linked choice into the default for new conversations', async () => {
    const { store } = await accessStore(true, true);
    store.setEnabled('s1', true);
    expect(localStorage.getItem(storageKeys(null).webMCPDefault)).toBeNull();
    expect(store.enabledFor('new')).toBe(false);
  });

  it('stops offering and running tools as soon as the grant is withdrawn', async () => {
    const { store, allowed, pageHost } = await accessStore(true, true);
    store.setEnabled('s1', true);
    expect(store.definitions('s1')).toHaveLength(1);
    allowed.value = false;
    expect(store.enabledFor('s1')).toBe(false);
    expect(store.definitions('s1')).toEqual([]);
    await expect(store.run(call, new AbortController().signal)).resolves.toMatchObject({
      ok: false,
    });
    expect(pageHost.execute).not.toHaveBeenCalled();
    // The remembered choice survives for when the grant returns.
    allowed.value = true;
    expect(store.definitions('s1')).toHaveLength(1);
  });

  it('does not pin a first send made while tools are not allowed', async () => {
    const { store, allowed } = await accessStore(true, false);
    expect(store.definitions('s1')).toEqual([]);
    allowed.value = true;
    store.setEnabled('s1', true);
    expect(store.definitions('s1')).toHaveLength(1);
  });

  it('keeps the owner profile unchanged: allowed, remembered default', async () => {
    const { store } = await accessStore(false, false);
    store.setEnabled('s1', true);
    expect(localStorage.getItem(storageKeys(null).webMCPDefault)).toBe('true');
    expect(store.enabledFor('new')).toBe(true);
    expect(store.definitions('new')).toHaveLength(1);
    await expect(store.run(call, new AbortController().signal)).resolves.toMatchObject({
      ok: true,
    });
  });
});

describe('AppStore device tools capability', () => {
  type Capabilities = { applyCapabilities(data: Record<string, unknown>, revision: number): void };
  const apply = (store: AppStore, data: Record<string, unknown>) =>
    (store as unknown as Capabilities).applyCapabilities(data, 0);
  const live = { enabled: true, transport: 'http_pcm', delegation_modes: ['server', 'client'] };

  const stores: AppStore[] = [];
  const create = (override: Partial<AppConfig> = {}) => {
    const store = new AppStore({ ...config, ...override });
    stores.push(store);
    return store;
  };
  afterEach(() => {
    stores.splice(0).forEach((store) => store.dispose());
  });

  it.each([
    ['absent', {}],
    ['false', { device_tools: false }],
    ['truthy but not true', { device_tools: 'true' }],
    ['numeric', { device_tools: 1 }],
  ])('keeps a linked device fail-closed when device_tools is %s', (_label, flag) => {
    const store = create();
    apply(store, { linked_devices: { active: true, mcp: true, ...flag }, live });
    expect(store.linkedDevicesMode.value).toBe(true);
    expect(store.linkedDeviceTools.value).toBe(false);
    expect(store.pageToolsAllowed.value).toBe(false);
    expect(store.liveStore.clientToolsAllowed.value).toBe(false);
  });

  it('honors an explicit device_tools grant for chat tools and Live delegation', () => {
    const store = create();
    apply(store, { linked_devices: { active: true, device_tools: true }, live });
    expect(store.linkedDeviceTools.value).toBe(true);
    expect(store.pageToolsAllowed.value).toBe(true);
    expect(store.liveStore.clientToolsAllowed.value).toBe(true);
  });

  it('honors the grant for an injected web-v1 profile', () => {
    const store = create({ linkedDevicesScope: 'web-v1' });
    apply(store, { linked_devices: { device_tools: true }, live });
    expect(store.linkedDevicesMode.value).toBe(true);
    expect(store.pageToolsAllowed.value).toBe(true);
  });

  it('ignores a device_tools flag when the page is not a linked device', () => {
    const store = create();
    apply(store, { linked_devices: { active: false, device_tools: true }, live });
    expect(store.linkedDevicesMode.value).toBe(false);
    expect(store.linkedDeviceTools.value).toBe(false);
    // The owner is unaffected.
    expect(store.pageToolsAllowed.value).toBe(true);
    expect(store.liveStore.clientToolsAllowed.value).toBe(true);
  });

  it('revokes a grant the next capabilities refresh no longer carries', () => {
    const store = create();
    apply(store, { linked_devices: { active: true, device_tools: true }, live });
    store.setWebMCPEnabled(true);
    apply(store, { linked_devices: { active: true }, live });
    expect(store.linkedDeviceTools.value).toBe(false);
    expect(store.pageToolsAllowed.value).toBe(false);
    expect(store.liveStore.clientToolsAllowed.value).toBe(false);
    expect(store.webMCPEnabled.value).toBe(false);
  });

  it('keeps the owner profile allowed with the default Live modes', () => {
    const store = create();
    apply(store, { live: { enabled: true } });
    expect(store.pageToolsAllowed.value).toBe(true);
    expect(store.liveStore.clientToolsAllowed.value).toBe(true);
  });
});

describe('linked chat requests', () => {
  const lastTools = (store: AppStore) => {
    const body = vi.mocked(store.endpoints.createResponse).mock.calls[0]?.[0] as
      Record<string, unknown> | undefined;
    return body?.tools;
  };

  async function linkedStore(deviceTools: boolean): Promise<AppStore> {
    const context = Object.assign(new EventTarget(), {
      getTools: vi.fn(async () => [
        { name: 'ping', description: 'Ping', inputSchema: { type: 'object', properties: {} } },
      ]),
      executeTool: vi.fn(async () => 'pong'),
    });
    Object.defineProperty(document, 'modelContext', { value: context, configurable: true });
    (window as TestWindow).__termLLMDeviceTools = { device: 'iPhone' };
    const store = new AppStore(config);
    store.sessions.value = [];
    store.activeSessionId.value = '';
    store.draftActive.value = true;
    store.endpoints.selectedSession = vi.fn(async () => ({}));
    store.endpoints.sessionState = vi.fn(async () => ({}));
    vi.spyOn(store, 'reconcile').mockResolvedValue(undefined);
    await vi.waitFor(() => expect(store.webMCP.available.value).toBe(true));
    (
      store as unknown as { applyCapabilities(d: Record<string, unknown>, r: number): void }
    ).applyCapabilities(
      { linked_devices: { active: true, ...(deviceTools ? { device_tools: true } : {}) } },
      0,
    );
    store.endpoints.createResponse = vi.fn(
      async () =>
        new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              const encoder = new TextEncoder();
              const frames: [string, Record<string, unknown>][] = [
                ['response.created', { response: { id: 'r1', status: 'in_progress' } }],
                ['response.output_text.delta', { delta: 'ok', output_index: 0 }],
                [
                  'response.completed',
                  { response: { id: 'r1', status: 'completed' }, final_rev: 1 },
                ],
              ];
              frames.forEach(([type, payload], index) =>
                controller.enqueue(
                  encoder.encode(
                    `event: ${type}\ndata: ${JSON.stringify({
                      ...payload,
                      response_id: 'r1',
                      run_epoch: 1,
                      sequence_number: index + 1,
                    })}\n\n`,
                  ),
                ),
              );
              controller.enqueue(encoder.encode('data: [DONE]\n\n'));
              controller.close();
            },
          }),
          { headers: { 'x-response-id': 'r1', 'x-session-id': 's1' } },
        ),
    );
    return store;
  }

  beforeEach(() => localStorage.clear());
  afterEach(() => {
    delete (document as { modelContext?: unknown }).modelContext;
    delete (window as TestWindow).__termLLMDeviceTools;
  });

  it('never sends page tools from a legacy linked device, even when toggled on', async () => {
    const store = await linkedStore(false);
    store.setWebMCPEnabled(true);
    store.prompt.value = 'Hello';
    await store.send();
    expect(lastTools(store)).toBeUndefined();
    store.dispose();
  });

  it('sends nothing from a granted linked device until the conversation opts in', async () => {
    const store = await linkedStore(true);
    store.prompt.value = 'Hello';
    await store.send();
    expect(lastTools(store)).toBeUndefined();
    store.dispose();
  });

  it('sends page tools from a granted linked device once the conversation opts in', async () => {
    const store = await linkedStore(true);
    store.setWebMCPEnabled(true);
    store.prompt.value = 'Hello';
    await store.send();
    expect(lastTools(store)).toEqual([expect.objectContaining({ name: 'webmcp__ping' })]);
    store.dispose();
  });
});
