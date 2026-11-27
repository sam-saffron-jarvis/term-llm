import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppStore } from './app-store';
import { testConfig, testSession } from './store-test-fixtures';

vi.mock('./mcp-operations', () => {
  throw new Error('MCP chunk unavailable');
});
vi.mock('./worktree-operations', () => {
  throw new Error('Worktree chunk unavailable');
});
vi.mock('./skill-run-operations', () => {
  throw new Error('Skill chunk unavailable');
});

const stores: AppStore[] = [];
function fixture() {
  localStorage.clear();
  const store = new AppStore(testConfig);
  stores.push(store);
  return store;
}

afterEach(() => {
  stores.splice(0).forEach((store) => store.dispose());
  vi.restoreAllMocks();
});

describe('operation chunk failures', () => {
  it('closes the reserved MCP popup, reports the error and does not start OAuth', async () => {
    const store = fixture();
    const close = vi.fn();
    vi.spyOn(window, 'open').mockReturnValue({ close } as unknown as Window);
    const start = vi.spyOn(store.endpoints, 'startMCPOAuth');
    await store.startMCPOAuth('server');
    expect(close).toHaveBeenCalledOnce();
    expect(start).not.toHaveBeenCalled();
    expect(store.mcp.value.oauth?.server).toBeUndefined();
    expect(store.toasts.value).toContainEqual(
      expect.objectContaining({
        kind: 'error',
        message: 'Could not load MCP controls. Reload the page to retry.',
      }),
    );
  });

  it('leaves the first message unsent if its pending MCP edit cannot load', async () => {
    const store = fixture();
    const setMCP = vi.spyOn(store.endpoints, 'setMCP');
    const createResponse = vi.spyOn(store.endpoints, 'createResponse');
    store.prompt.value = 'Keep this draft';
    const toggling = store.toggleMCP('server');
    await store.send();
    await toggling;
    expect(setMCP).not.toHaveBeenCalled();
    expect(createResponse).not.toHaveBeenCalled();
    expect(store.prompt.value).toBe('Keep this draft');
    expect(store.toasts.value).toContainEqual(
      expect.objectContaining({
        kind: 'error',
        message: 'MCP settings were not applied. Message not sent.',
      }),
    );
  });

  it('sets the load error and rejects a worktree mutation without using its endpoint', async () => {
    const store = fixture();
    const list = vi.spyOn(store.endpoints, 'legacyWorktrees');
    const create = vi.spyOn(store.api, 'post');
    await store.loadWorktrees();
    expect(store.worktreeError.value).toBe(
      'Could not load worktree controls. Reload the page to retry.',
    );
    await expect(store.createWorktree('test')).rejects.toThrow('Could not load worktree controls.');
    expect(list).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
  });

  it('restores the skill invocation without materializing a draft or dropping its arguments', async () => {
    const store = fixture();
    const create = vi.spyOn(store.endpoints, 'createBlankSession');
    const invoke = vi.spyOn(store.endpoints, 'invokeSkill');
    await store.invokeSkill('review', 'src');
    expect(store.prompt.value).toBe('/review src');
    expect(create).not.toHaveBeenCalled();
    expect(invoke).not.toHaveBeenCalled();
    expect(store.toasts.value).toContainEqual(
      expect.objectContaining({
        kind: 'error',
        message: 'Could not load skill controls. Reload the page to retry.',
      }),
    );
  });

  it('unlocks cancellation if skill controls could not be loaded', async () => {
    const store = fixture();
    store.sessions.value = [testSession()];
    store.activeSessionId.value = 's1';
    store.draftActive.value = false;
    const cancel = vi.spyOn(store.endpoints, 'cancelSkillRun');
    const cancelling = store.cancelSkill('run-1');
    expect(store.skillStore.cancelling.value.has('run-1')).toBe(true);
    await cancelling;
    expect(store.skillStore.cancelling.value.size).toBe(0);
    expect(cancel).not.toHaveBeenCalled();
    expect(store.toasts.value).toContainEqual(expect.objectContaining({ kind: 'error' }));
  });
});
