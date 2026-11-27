import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppStore } from './app-store';
import { deferred, testConfig, testSession } from './store-test-fixtures';

const chunks = vi.hoisted(() => {
  const gate = () => {
    let release!: () => void;
    const ready = new Promise<void>((resolve) => (release = resolve));
    return { ready, release };
  };
  return { mcp: gate(), worktree: gate(), skill: gate() };
});

vi.mock('./mcp-operations', async (actual) => {
  await chunks.mcp.ready;
  return actual();
});
vi.mock('./worktree-operations', async (actual) => {
  await chunks.worktree.ready;
  return actual();
});
vi.mock('./skill-run-operations', async (actual) => {
  await chunks.skill.ready;
  return actual();
});

const stores: AppStore[] = [];
function fixture() {
  const store = new AppStore(testConfig);
  stores.push(store);
  localStorage.clear();
  return store;
}

afterEach(() => {
  stores.splice(0).forEach((store) => store.dispose());
  vi.restoreAllMocks();
});

describe('first action with cold store operations', () => {
  it('opens OAuth synchronously and saves the draft selection before sending', async () => {
    const store = fixture();
    const popup = { location: { assign: vi.fn() }, close: vi.fn() };
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window);
    const draftId = store.composer.runtimeDraftId();
    const save = deferred<{ servers: []; enabled: string[] }>();
    store.endpoints.setMCP = vi.fn(() => save.promise);
    store.endpoints.startMCPOAuth = vi.fn(async () => ({
      flow_id: 'flow',
      state: 'pending' as const,
      authorization_url: 'https://auth.example/authorize',
      expires_at: '',
    }));
    store.endpoints.getMCP = vi.fn(async () => ({ servers: [], enabled: [] }));
    store.endpoints.cancelMCPOAuth = vi.fn(async () => ({}));
    store.endpoints.createResponse = vi.fn(async () => {
      throw new Error('Response endpoint reached');
    });

    const authorizing = store.startMCPOAuth('server');
    expect(open).toHaveBeenCalledOnce();
    expect(store.mcp.value.oauth?.server?.state).toBe('starting');
    expect(store.endpoints.startMCPOAuth).not.toHaveBeenCalled();
    const toggling = store.toggleMCP('server');
    store.prompt.value = 'First message';
    const sending = store.send();
    expect(store.endpoints.setMCP).not.toHaveBeenCalled();
    expect(store.endpoints.createResponse).not.toHaveBeenCalled();

    chunks.mcp.release();
    await authorizing;
    expect(popup.location.assign).toHaveBeenCalledWith('https://auth.example/authorize');
    expect(store.endpoints.setMCP).toHaveBeenCalledWith(draftId, ['server']);
    expect(store.endpoints.createResponse).not.toHaveBeenCalled();
    save.resolve({ servers: [], enabled: ['server'] });
    await toggling;
    await sending;
    expect(store.endpoints.createResponse).toHaveBeenCalledWith(
      expect.any(Object),
      draftId,
      expect.any(String),
      expect.any(AbortSignal),
    );
    await store.cancelMCPOAuth('server');
  });

  it('keeps draft selection synchronous and preserves the first create request', async () => {
    const store = fixture();
    store.projectsEnabled.value = true;
    store.activeProjectId.value = 'project-1';
    store.chooseDraftWorktree('/trees/original');
    expect(store.currentWorktreeDir.value).toBe('/trees/original');
    store.endpoints.createProjectWorktree = vi.fn(async () => ({
      worktree: { dir: '/trees/new' },
    }));
    store.endpoints.projectWorktrees = vi.fn(async () => ({ worktrees: [] }));

    const creating = store.createWorktree('new', true);
    expect(store.endpoints.createProjectWorktree).not.toHaveBeenCalled();
    chunks.worktree.release();
    await creating;
    expect(store.endpoints.createProjectWorktree).toHaveBeenCalledExactlyOnceWith('project-1', {
      name: 'new',
      clean: true,
    });
    expect(store.currentWorktreeDir.value).toBe('/trees/new');
  });

  it('loads the skill catalogue without waiting and starts the first invocation after import', async () => {
    const store = fixture();
    store.sessions.value = [testSession()];
    store.activeSessionId.value = 's1';
    store.draftActive.value = false;
    store.endpoints.skills = vi.fn(async () => ({
      skills: [{ name: 'review', execution: 'inline' }],
    }));
    store.endpoints.invokeSkill = vi.fn(async () => ({ response_id: 'r1', run_epoch: 1 }));
    vi.spyOn(store, 'streamResponse').mockResolvedValue(undefined);
    await store.loadSkills();
    expect(store.skills.value).toEqual([{ name: 'review', execution: 'inline' }]);

    const invoking = store.invokeSkill('review', 'src');
    expect(store.endpoints.invokeSkill).not.toHaveBeenCalled();
    chunks.skill.release();
    await invoking;
    expect(store.endpoints.invokeSkill).toHaveBeenCalledExactlyOnceWith(
      's1',
      expect.objectContaining({ name: 'review', arguments: 'src' }),
      expect.any(String),
      true,
    );
    expect(store.streamResponse).toHaveBeenCalledWith('r1', 's1', 0);
  });
});
