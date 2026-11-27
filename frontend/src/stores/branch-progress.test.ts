import { signal } from '@preact/signals';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../api/client';
import type { WirePathNotesStatus, WirePreparePathNotesResponse } from '../api/generated/types';
import type { Session } from '../domain/types';
import type { AppStoreServices } from './app-store-services';
import { BranchProgress, type NotesJob } from './branch-progress';
import { deferred } from './store-test-fixtures';

const edge = { sourceId: 'source', anchor: 42 };
function fixture() {
  const id = signal('child');
  const active = signal<Session | null>(null);
  const tree = signal({
    nodes: [{ session_id: 'child', parent_session_id: 'source', fork_after_message_id: 42 }],
  });
  const jobs = signal<ReadonlyMap<string, NotesJob>>(new Map());
  const load = vi.fn<() => Promise<void>>();
  const endpoints = {
    pathNotes: vi.fn<() => Promise<WirePreparePathNotesResponse>>(),
    pathNotesStatus: vi.fn<() => Promise<WirePathNotesStatus>>(),
    cancelPathNotes: vi.fn(),
    selectedSession: vi.fn(),
  };
  const toast = vi.fn();
  const progress = new BranchProgress(
    jobs,
    { endpoints, toast } as unknown as AppStoreServices,
    id,
    active,
    tree,
    load,
  );
  const install = (rev: number) => {
    active.value = {
      id: 'child',
      messageBodiesRev: rev,
      messages: [
        {
          role: 'path-note',
          provenance: { source_session_id: edge.sourceId, anchor_message_id: edge.anchor },
        },
      ],
    } as Session;
  };
  return { progress, endpoints, load, id, toast, install };
}

afterEach(() => vi.useRealTimers());

describe('BranchProgress', () => {
  it('re-fetches for the highest queued revision without caching a saved result', async () => {
    const { progress, load, install } = fixture();
    const first = deferred<void>();
    load.mockReturnValueOnce(first.promise).mockImplementationOnce(async () => install(4));
    const old = progress.reconcile('child', 1);
    const newer = progress.reconcile('child', 3);
    const latest = progress.reconcile('child', 4);
    expect(load).toHaveBeenCalledOnce();
    install(1);
    first.resolve();
    expect(await Promise.all([old, newer, latest])).toEqual([true, true, true]);
    expect(load).toHaveBeenCalledTimes(2);
    load
      .mockRejectedValueOnce(new Error('read unavailable'))
      .mockImplementationOnce(async () => install(5));
    expect(await progress.reconcile('child', 5)).toBe(false);
    expect(await progress.reconcile('child', 5)).toBe(true);
    expect(load).toHaveBeenCalledTimes(4);
    progress.dispose();
  });

  it('preserves the limited-output toast when the durable event beats POST', async () => {
    const { progress, endpoints, toast, install } = fixture();
    const post = deferred<WirePreparePathNotesResponse>();
    endpoints.pathNotes.mockReturnValue(post.promise);
    const job = progress.begin('child', 'notes', '', edge, true);
    install(2);
    expect(await progress.onTranscriptChanged('child', 2)).toBe(true);
    expect(await job.done).toBe('saved');
    post.resolve({
      ready: true,
      note_saved: true,
      limited: true,
      message: 'Some output was omitted.',
    });
    await post.promise;
    expect(toast).toHaveBeenCalledExactlyOnceWith('Some output was omitted.', 'info');
    progress.dispose();
  });

  it('waits for a saving snapshot and reports that save won only once', async () => {
    vi.useFakeTimers();
    const { progress, endpoints, toast, install } = fixture();
    const post = deferred<WirePreparePathNotesResponse>();
    const status = deferred<WirePathNotesStatus>();
    endpoints.pathNotes.mockReturnValue(post.promise);
    endpoints.pathNotesStatus.mockReturnValue(status.promise);
    endpoints.cancelPathNotes.mockResolvedValue({
      cancelled: false,
      note_saved: false,
      status: { state: 'saving' },
    });
    const job = progress.begin('child', 'notes', '', edge, true);
    await vi.advanceTimersByTimeAsync(700);
    progress.skip(job);
    await job.skipRequest;
    expect(job.state.peek()).toMatchObject({ skipping: true, status: { state: 'saving' } });
    install(2);
    expect(await progress.onTranscriptChanged('child', 2)).toBe(true);
    expect(await job.done).toBe('saved');
    status.resolve({ state: 'idle' });
    post.resolve({ ready: true, note_saved: true });
    await post.promise;
    expect(endpoints.cancelPathNotes).toHaveBeenCalledOnce();
    expect(toast).toHaveBeenCalledExactlyOnceWith(expect.stringContaining('already saved'), 'info');
    progress.dispose();
  });

  it('replaces an existing poller and rejects a different anchor on the same source', async () => {
    vi.useFakeTimers();
    const { progress, endpoints, id } = fixture();
    id.value = 'source';
    endpoints.pathNotes.mockRejectedValue(new APIError('already active', 409));
    endpoints.pathNotesStatus.mockResolvedValue({ state: 'idle' });
    endpoints.selectedSession.mockResolvedValue({
      selected_session: { id: 'child', branch_parent_session_id: 'source' },
      selected_transcript: {
        bodies: {
          messages: [
            {
              role: 'path-note',
              parts: [{ path_note: { source_session_id: 'source', anchor_message_id: 99 } }],
            },
          ],
        },
      },
    });
    const first = progress.begin('child', 'notes', '', edge);
    const next = progress.begin('child', 'notes', '', edge);
    expect(await first.done).toBe('detached');
    expect(first.controller.signal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(700);
    expect(await next.done).toBe('failed');
    expect(endpoints.pathNotesStatus).toHaveBeenCalledOnce();
    progress.dispose();
  });
});
