import { effect, signal, type ReadonlySignal, type Signal } from '@preact/signals';
import { APIError } from '../api/client';
import type { WirePathNotesStatus } from '../api/generated/types';
import { listFrom, recordValue } from './store-utils';
import { forkEdge } from '../domain/fork';
import type { Session } from '../domain/types';
import { errorMessage } from '../domain/text';
import type { AppStoreServices } from './app-store-services';

export type NotesMode = 'notes' | 'focused';
export type NotesOutcome = 'saved' | 'empty' | 'skipped' | 'failed' | 'detached';
export type NoteEdge = { sourceId: string; anchor: number };
export type NotesState = {
  status: WirePathNotesStatus;
  outcome?: NotesOutcome;
  writingObservedAt?: number;
  endedAt?: number;
  error?: string;
  skipping?: boolean;
};

export type NotesJob = {
  id: string;
  mode: NotesMode;
  focus: string;
  edge: NoteEdge;
  state: Signal<NotesState>;
  controller: AbortController;
  timer?: ReturnType<typeof setTimeout>;
  posting: boolean;
  reloaded: boolean;
  background: boolean;
  knownSaved?: boolean;
  skipRequest?: Promise<void>;
  completing?: boolean;
  done: Promise<NotesOutcome>;
  finish: (outcome: NotesOutcome) => void;
};

export type BranchStep = {
  mode: NotesMode;
  source: number;
  started: number;
  child?: number;
  created?: number;
  opening?: number;
  opened?: number;
  job?: NotesJob;
  action: { early?: 'open' | 'skip' };
};

type Reconciliation = { rev: number; promise: Promise<boolean> };

function hasPathNote(response: Record<string, unknown>, id: string, edge: NoteEdge): boolean {
  const selected = recordValue(response.selected_session);
  const bodies = recordValue(recordValue(response.selected_transcript)?.bodies);
  return (
    selected?.id === id &&
    listFrom(bodies || {}, 'messages', 'items').some((row) => {
      const message = recordValue(row);
      return (
        message?.role === 'path-note' &&
        listFrom(message, 'parts').some((part) => {
          const note = recordValue(recordValue(part)?.path_note);
          return (
            note?.source_session_id === edge.sourceId &&
            (Number(note.anchor_message_id) || 0) === edge.anchor
          );
        })
      );
    })
  );
}

function noteDeferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

export class BranchProgress {
  private readonly reconciliations = new Map<string, Reconciliation>();
  private disposed = false;

  constructor(
    readonly jobs: Signal<ReadonlyMap<string, NotesJob>>,
    private readonly services: AppStoreServices,
    private readonly activeSessionId: ReadonlySignal<string>,
    private readonly activeSession: ReadonlySignal<Session | null>,
    private readonly tree: ReadonlySignal<Record<string, unknown> | null>,
    private readonly loadSession: (id: string) => Promise<void>,
  ) {}

  private active(id: string): boolean {
    return this.activeSessionId.peek() === id;
  }

  private edge(id: string): NoteEdge | undefined {
    const edge = forkEdge(this.tree.peek(), id);
    if (edge) return { sourceId: edge.source.id, anchor: edge.anchor };
  }

  private async reload(id: string, rev: number): Promise<boolean> {
    const hasNote = () => {
      const session = this.activeSession.peek();
      const edge = this.jobs.peek().get(id)?.edge ?? this.edge(id);
      return (
        !!edge &&
        session?.id === id &&
        (!rev || (session.messageBodiesRev ?? 0) >= rev) &&
        session.messages.some(
          (message) =>
            message.role === 'path-note' &&
            message.provenance?.source_session_id === edge.sourceId &&
            (message.provenance.anchor_message_id ?? 0) === edge.anchor,
        )
      );
    };
    if (!hasNote()) await this.loadSession(id);
    return hasNote();
  }

  private create(
    id: string,
    mode: NotesMode,
    focus: string,
    edge: NoteEdge,
    status: WirePathNotesStatus,
    reloaded: boolean,
  ): NotesJob {
    const existing = this.jobs.peek().get(id);
    if (existing) this.stop(existing);
    const { promise, resolve } = noteDeferred<NotesOutcome>();
    const job: NotesJob = {
      id,
      mode,
      focus,
      edge,
      state: signal<NotesState>({ status }),
      controller: new AbortController(),
      posting: !reloaded,
      reloaded,
      background: reloaded,
      done: promise,
      finish: resolve,
    };
    this.jobs.value = new Map(this.jobs.peek()).set(id, job);
    if (this.disposed) this.stop(job);
    return job;
  }

  begin(id: string, mode: NotesMode, focus: string, edge: NoteEdge, background = false): NotesJob {
    const job = this.create(id, mode, focus, edge, { state: 'reading' }, false);
    job.background = background;
    if (!job.controller.signal.aborted) void this.post(job);
    this.schedule(job);
    return job;
  }

  resume(id: string, status: WirePathNotesStatus): void {
    const existing = this.jobs.peek().get(id);
    if (
      this.disposed ||
      !this.active(id) ||
      !['reading', 'writing', 'saving'].includes(status.state) ||
      (existing && !existing.state.peek().outcome)
    )
      return;
    const edge = this.edge(id);
    if (!edge) return;
    const job = this.create(
      id,
      status.mode === 'focused' ? 'focused' : 'notes',
      status.focus || '',
      edge,
      status,
      true,
    );
    this.applyStatus(job, status);
    this.schedule(job);
  }

  private schedule(job: NotesJob): void {
    clearTimeout(job.timer);
    if (!job.controller.signal.aborted) job.timer = setTimeout(() => void this.poll(job), 700);
  }

  private applyStatus(job: NotesJob, status: WirePathNotesStatus): void {
    const current = job.state.peek();
    job.state.value = {
      ...current,
      status,
      writingObservedAt:
        current.writingObservedAt ??
        (status.state === 'writing' || status.state === 'saving' ? performance.now() : undefined),
    };
  }

  private async poll(job: NotesJob): Promise<void> {
    if (job.controller.signal.aborted) return;
    try {
      if (job.knownSaved) {
        await this.complete(job, 'saved');
        return;
      }
      const status = await this.services.endpoints.pathNotesStatus(job.id, job.controller.signal);
      if (job.controller.signal.aborted) return;
      if (status.state !== 'idle') this.applyStatus(job, status);
      else if (!job.posting && !job.skipRequest) {
        // An attached job may finish while its modal is still on the source.
        const saved = this.active(job.id)
          ? await this.reconcile(job.id)
          : !job.reloaded &&
            hasPathNote(await this.services.endpoints.selectedSession(job.id), job.id, job.edge);
        if (job.controller.signal.aborted) return;
        await this.complete(
          job,
          saved
            ? 'saved'
            : job.reloaded || (job.background && !this.active(job.id))
              ? 'detached'
              : 'failed',
          '',
          saved,
        );
        return;
      }
    } catch (error) {
      if (error instanceof APIError && [401, 403, 404].includes(error.status)) {
        await this.complete(job, 'detached');
        return;
      }
    }
    this.schedule(job);
  }

  private async post(job: NotesJob): Promise<void> {
    try {
      const result = await this.services.endpoints.pathNotes(job.id, {
        mode: job.mode,
        ...(job.mode === 'focused' ? { focus: job.focus } : {}),
      });
      if (!this.disposed && result.limited)
        this.services.toast(
          result.message ||
            'Some source output was omitted from the new path context because it was not available yet.',
          'info',
        );
      if (job.skipRequest) await job.skipRequest;
      await this.complete(
        job,
        result.note_saved ? 'saved' : result.cancelled ? 'skipped' : 'empty',
      );
    } catch (error) {
      if (job.controller.signal.aborted) return;
      if (
        error instanceof APIError &&
        error.status === 409 &&
        error.message.includes('already active')
      ) {
        job.posting = false;
        return;
      }
      if (job.skipRequest) await job.skipRequest;
      await this.complete(job, 'failed', errorMessage(error));
    }
  }

  skip(job: NotesJob): void {
    const state = job.state.peek();
    if (state.outcome || state.skipping || job.controller.signal.aborted) return;
    job.state.value = { ...state, skipping: true };
    job.skipRequest = this.cancel(job).finally(() => {
      job.skipRequest = undefined;
    });
  }

  async skipBeforeStart(id: string): Promise<void> {
    try {
      const result = await this.services.endpoints.cancelPathNotes(id);
      if (!this.disposed && result.note_saved) this.savedBeforeSkip();
    } catch (error) {
      if (!this.disposed) this.services.toast(error, 'error');
    }
  }

  private savedBeforeSkip(): void {
    this.services.toast('Path notes were already saved. The path opens with its notes.', 'info');
  }

  private async cancel(job: NotesJob): Promise<void> {
    try {
      const result = await this.services.endpoints.cancelPathNotes(job.id);
      if (job.controller.signal.aborted) return;
      if (result.status) this.applyStatus(job, result.status);
      else if (result.note_saved) await this.complete(job, 'saved');
      else if (result.cancelled) await this.complete(job, 'skipped');
    } catch (error) {
      if (job.controller.signal.aborted) return;
      job.state.value = { ...job.state.peek(), skipping: false };
      this.services.toast(error, 'error');
    }
  }

  private async complete(
    job: NotesJob,
    outcome: NotesOutcome,
    detail = '',
    verified = false,
  ): Promise<void> {
    if (job.controller.signal.aborted || job.completing) return;
    job.completing = true;
    const endedAt = performance.now();
    if (outcome === 'saved') {
      job.knownSaved = true;
      if (
        !verified &&
        this.active(job.id) &&
        !(await this.reconcile(job.id)) &&
        this.active(job.id)
      ) {
        job.completing = false;
        this.schedule(job);
        return;
      }
    }
    if (job.controller.signal.aborted) return;
    job.controller.abort();
    clearTimeout(job.timer);
    const current = job.state.peek();
    if (outcome === 'saved' && current.skipping) this.savedBeforeSkip();
    const error = outcome === 'failed' ? detail || 'Notes couldn’t be prepared.' : undefined;
    job.state.value = { ...current, outcome, endedAt, error, skipping: false };
    if (job.background && error)
      this.services.toast(
        `New path created, but its additional context could not be prepared: ${error}`,
        'error',
      );
    if (outcome !== 'failed') this.dismiss(job);
    job.finish(outcome);
  }

  async onTranscriptChanged(id: string, rev?: number): Promise<boolean> {
    const job = this.jobs.peek().get(id);
    const saved = await this.reconcile(id, rev);
    if (saved && job && !job.skipRequest) {
      if (job.state.peek().outcome === 'failed') this.dismiss(job);
      else await this.complete(job, 'saved', '', true);
    }
    return saved;
  }

  reconcile(id: string, rev = 0): Promise<boolean> {
    if (this.disposed || !this.active(id)) return Promise.resolve(false);
    const current = this.reconciliations.get(id);
    if (current) {
      current.rev = Math.max(current.rev, rev);
      return current.promise;
    }
    const pending: Reconciliation = { rev, promise: Promise.resolve(false) };
    this.reconciliations.set(id, pending);
    pending.promise = this.reconcileThrough(id, pending);
    return pending.promise;
  }

  private async reconcileThrough(id: string, pending: Reconciliation): Promise<boolean> {
    while (!this.disposed && this.active(id)) {
      const requested = pending.rev;
      const saved = await this.reload(id, requested).catch(() => false);
      if (pending.rev === requested) {
        this.reconciliations.delete(id);
        return saved;
      }
    }
    this.reconciliations.delete(id);
    return false;
  }

  retry(job: NotesJob): NotesJob | undefined {
    if (this.jobs.peek().get(job.id) !== job || job.state.peek().outcome !== 'failed') return;
    return this.begin(job.id, job.mode, job.focus, job.edge, job.background);
  }

  private stop(job: NotesJob): void {
    job.controller.abort();
    clearTimeout(job.timer);
    job.finish('detached');
  }

  dismiss(job: NotesJob): void {
    if (this.jobs.peek().get(job.id) !== job) return;
    const next = new Map(this.jobs.peek());
    next.delete(job.id);
    this.jobs.value = next;
    if (!job.controller.signal.aborted) this.stop(job);
  }

  dispose(): void {
    this.disposed = true;
    for (const job of this.jobs.peek().values()) this.stop(job);
  }
}

type NotesModal = {
  initial: BranchStep;
  owns: () => boolean;
  step: Signal<BranchStep | null>;
  close: () => void;
  waitForAction: (fn?: () => void) => void;
  bindRetry: (fn?: () => void) => void;
};

export async function trackNotes(
  progress: BranchProgress,
  id: string,
  number: number,
  mode: NotesMode,
  focus: string,
  edge: NoteEdge,
  open: () => Promise<void>,
  modal?: NotesModal,
): Promise<void> {
  const owned = modal?.owns() === true;
  const skipEarly = modal?.initial.action.early === 'skip';
  let job = skipEarly ? undefined : progress.begin(id, mode, focus, edge, !owned);
  if (skipEarly) {
    const cancelling = progress.skipBeforeStart(id);
    if (owned) await cancelling;
  }
  if (!modal) {
    await job?.done;
    await open();
    return;
  }
  if (!modal.owns()) return;
  const publish = (change: Partial<BranchStep>) => {
    const step = modal.step.peek();
    if (step && modal.owns()) modal.step.value = { ...step, ...change };
  };
  publish({ child: number, created: performance.now(), job });
  try {
    while (job && modal.owns()) {
      const current = job;
      const early = modal.initial.action.early;
      if (early === 'open') {
        current.background = true;
        modal.close();
        await open();
        return;
      }
      const action = noteDeferred<void>();
      modal.waitForAction(action.resolve);
      const stop = effect(() => {
        if (!modal.owns()) action.resolve();
      });
      const outcome = await Promise.race([current.done, action.promise]);
      stop();
      if (outcome == null) continue;
      if (outcome !== 'failed') break;
      const retry = noteDeferred<NotesJob | undefined>();
      modal.waitForAction(() => retry.resolve(undefined));
      modal.bindRetry(() => {
        modal.bindRetry(undefined);
        retry.resolve(progress.retry(current));
      });
      const stopRetry = effect(() => {
        if (!modal.owns()) retry.resolve(undefined);
      });
      job = await retry.promise;
      stopRetry();
      if (!job) {
        progress.dismiss(current);
        break;
      }
      modal.initial.action.early = undefined;
      publish({ job });
    }
    if (!modal.owns()) {
      if (job) job.background = true;
      return;
    }
    publish({ opening: performance.now() });
    await open();
    publish({ opened: performance.now() });
    modal.close();
  } finally {
    modal.waitForAction(undefined);
    modal.bindRetry(undefined);
  }
}
