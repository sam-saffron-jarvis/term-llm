import { signal, type ReadonlySignal, type Signal } from '@preact/signals';
import type { Attachment, Message, Session } from '../domain/types';
import { errorMessage } from '../domain/text';
import type { Modal } from './store-types';
import type { BranchProgress, BranchStep, NotesJob } from './branch-progress';
import type { WirePathNotesStatus } from '../api/generated/types';
import { uuid } from './store-utils';
import type { AppStoreServices } from './app-store-services';

export interface BranchStoreOptions {
  activeSession: ReadonlySignal<Session | null>;
  activeSessionId: ReadonlySignal<string>;
  draftActive: ReadonlySignal<boolean>;
  attachments: ReadonlySignal<Attachment[]>;
  visibleMessages: ReadonlySignal<Message[]>;
  prompt: Signal<string>;
  modal: Signal<Modal>;
  refreshSidebar: () => Promise<void>;
  publishSessionChange: () => void;
  findSession: (id: string) => Session | undefined;
  createSession: (value: Record<string, unknown>) => Session;
  prependSession: (session: Session) => void;
  selectSession: (session: Session) => Promise<void>;
  send: () => Promise<void>;
  loadSession: (id: string) => Promise<void>;
}

/** Owns branch tree presentation and branch/fork creation commands. */
export class BranchStore {
  readonly tree = signal<Record<string, unknown> | null>(null);
  readonly pathCount = signal(0);
  readonly target = signal('');
  readonly prefill = signal('');
  /** Message a /thread command sends once its chosen context is in place. */
  readonly autoSend = signal('');
  /** Composer text of the /thread command that opened the dialog. */
  private commandPrompt = '';
  readonly busy = signal(false);
  readonly error = signal('');

  readonly notesJobs = signal<ReadonlyMap<string, NotesJob>>(new Map());
  readonly step = signal<BranchStep | null>(null);
  private readonly resuming = signal('');
  private progress?: BranchProgress;
  private disposed = false;
  private openEarly?: () => void;
  private modalEpoch = 0;
  private retryOperation: { signature: string; idempotencyKey: string } | null = null;

  constructor(
    private readonly services: AppStoreServices,
    private readonly options: BranchStoreOptions,
  ) {}

  private async loadProgress() {
    const module = await import('./branch-progress');
    const progress = (this.progress ??= new module.BranchProgress(
      this.notesJobs,
      this.services,
      this.options.activeSessionId,
      this.options.activeSession,
      this.tree,
      this.options.loadSession,
    ));
    if (this.disposed) progress.dispose();
    return { module, progress };
  }

  pending(id: string): boolean {
    const job = this.notesJobs.value.get(id);
    return (!!job && !job.state.value.outcome) || (!!id && this.resuming.value === id);
  }

  async notesChanged(id: string, rev?: number): Promise<boolean> {
    if (this.disposed || this.options.activeSessionId.peek() !== id) return false;
    const { progress } = await this.loadProgress();
    return progress.onTranscriptChanged(id, rev);
  }

  dispose(): void {
    this.disposed = true;
    this.progress?.dispose();
  }

  async refresh(
    sessionId = this.options.activeSessionId.peek(),
    includeBranchPoints = false,
    reportError = false,
  ): Promise<Record<string, unknown> | null> {
    if (!sessionId) {
      this.tree.value = null;
      this.pathCount.value = 0;
      return null;
    }
    try {
      const tree = includeBranchPoints
        ? await this.services.endpoints.tree(sessionId, undefined, true)
        : await this.services.endpoints.tree(sessionId);
      if (this.options.activeSessionId.peek() !== sessionId) return null;

      this.tree.value = tree;
      this.pathCount.value = Math.max(1, Number(tree.path_count) || 1);
      const status = tree.path_notes as WirePathNotesStatus | undefined;
      if (status && status.state !== 'idle' && !this.pending(sessionId)) {
        this.resuming.value = sessionId;
        try {
          const { progress } = await this.loadProgress();
          progress.resume(sessionId, status);
        } finally {
          if (this.resuming.peek() === sessionId) this.resuming.value = '';
        }
      }
      return tree;
    } catch (error) {
      if (this.options.activeSessionId.peek() === sessionId) {
        if (reportError) this.services.toast(error, 'error');
        this.tree.value = null;
        this.pathCount.value = 0;
      }
      return null;
    }
  }

  async load(): Promise<void> {
    const sessionId = this.options.activeSessionId.peek();
    if (!sessionId) {
      this.tree.value = null;
      this.pathCount.value = 0;
      this.services.toast('Start a conversation before opening its paths.', 'error');
      return;
    }
    const tree = await this.refresh(sessionId, true, true);
    if (tree) this.options.modal.value = 'branch';
  }

  async command(kind: 'fork' | 'thread', message = ''): Promise<void> {
    const session = this.options.activeSession.value;
    if (!session || this.options.draftActive.value) {
      this.services.toast('Start the conversation before creating a thread or fork.', 'error');
      return;
    }
    if (this.busy.peek()) {
      this.services.toast('A conversation path is already being created.', 'info');
      return;
    }
    if (this.options.attachments.value.length) {
      this.services.toast('Create the thread or fork before attaching files or images.', 'error');
      return;
    }
    if (kind === 'thread') {
      // A thread starts from anchor zero: the whole conversation is "later",
      // so it offers the same context choices as forking from a message.
      this.openContext('0', '', message.trim());
      this.commandPrompt = this.options.prompt.peek();
      return;
    }
    const anchor =
      [...this.options.visibleMessages.value]
        .reverse()
        .find((entry) => Number(entry.durableRowId) > 0)?.durableRowId || 0;
    const original = this.options.prompt.value;
    this.options.prompt.value = '';
    const created = await this.branchFrom(String(anchor), 'clean', '', message.trim());
    if (!created && !this.options.prompt.value) this.options.prompt.value = original;
  }

  openContext(messageId: string, prefill = '', autoSend = ''): void {
    this.modalEpoch++;
    this.step.value = null;
    this.target.value = messageId;
    this.prefill.value = prefill;
    this.autoSend.value = autoSend;
    this.commandPrompt = '';
    this.error.value = '';
    this.options.modal.value = 'branch-context';
  }

  /** Creates the path chosen in the context dialog. A /thread command leaves
   *  the composer while the path is created and returns to it on failure. */
  async choose(context: 'clean' | 'notes' | 'focused', focus = ''): Promise<boolean> {
    const autoSend = this.autoSend.peek();
    const original = this.commandPrompt;
    const ownsPrompt = !!original && this.options.prompt.peek() === original;
    if (ownsPrompt) this.options.prompt.value = '';
    const created = await this.branchFrom(
      this.target.peek(),
      context,
      focus,
      autoSend,
      this.prefill.peek(),
    );
    if (!created && ownsPrompt && !this.options.prompt.value) this.options.prompt.value = original;
    return created;
  }

  openNow(skip = false): void {
    const step = this.step.peek();
    if (!step || step.opening != null) return;
    if (skip && step.job) {
      this.skipNotes(step.job);
      return;
    }
    step.action.early = skip ? 'skip' : 'open';
    this.step.value = { ...step };
    this.openEarly?.();
  }

  retryNotes(job: NotesJob): void {
    this.progress?.retry(job);
  }

  skipNotes(job: NotesJob): void {
    this.progress?.skip(job);
  }

  dismissNotes(job: NotesJob): void {
    this.progress?.dismiss(job);
  }

  private async refreshCreatedPath(target: Session): Promise<Session> {
    try {
      await this.options.refreshSidebar();
    } catch (error) {
      this.services.toast(
        `New path created, but the sidebar could not refresh: ${errorMessage(error)}`,
        'error',
      );
    }
    const current = this.options.findSession(target.id);
    if (!current) this.options.prependSession(target);
    this.options.publishSessionChange();
    return current || target;
  }

  private async openPath(
    target: Session,
    sourceID: string,
    prefill: string,
    autoSend: string,
  ): Promise<void> {
    if (this.options.activeSessionId.peek() !== sourceID) return;
    try {
      await this.options.selectSession(target);
    } catch (error) {
      this.services.toast(
        `New path created, but it could not be opened: ${errorMessage(error)}`,
        'error',
      );
      return;
    }
    if (autoSend) {
      this.options.prompt.value = autoSend;
      try {
        await this.options.send();
      } catch (error) {
        this.services.toast(
          `New path created, but its first message could not be sent: ${errorMessage(error)}`,
          'error',
        );
      }
    } else if (prefill && this.options.activeSessionId.peek() === target.id)
      this.options.prompt.value = prefill;
  }

  retryModal?: () => void;

  async branchFrom(
    messageId: string,
    context: string,
    focus = '',
    autoSend = '',
    prefill = '',
  ): Promise<boolean> {
    if (this.busy.peek()) return false;
    const session = this.options.activeSession.value;
    if (!session) {
      this.services.toast('Select a conversation before creating a path.', 'error');
      return false;
    }

    const anchor = Number(messageId) || 0;
    const signature = `${session.id}\u0000${anchor}`;
    if (!this.retryOperation || this.retryOperation.signature !== signature)
      this.retryOperation = { signature, idempotencyKey: uuid() };
    const operation = this.retryOperation;
    const modalTarget = this.target.peek();
    const modalEpoch = this.modalEpoch;
    const ownsBranchModal =
      this.options.modal.peek() === 'branch-context' && modalTarget === messageId;
    const ownsModal = () =>
      ownsBranchModal &&
      this.modalEpoch === modalEpoch &&
      this.options.modal.value === 'branch-context' &&
      this.target.value === modalTarget;
    const finishBranchModal = () => {
      if (ownsModal()) {
        this.target.value = '';
        this.prefill.value = '';
        this.autoSend.value = '';
        this.step.value = null;
        this.options.modal.value = '';
      }
    };
    const mode = context === 'notes' || context === 'focused' ? context : null;
    const initial: BranchStep | undefined =
      mode && ownsBranchModal
        ? { mode, source: session.number || 0, started: performance.now(), action: {} }
        : undefined;
    if (initial) this.step.value = initial;
    let createdID = '';
    this.busy.value = true;
    this.error.value = '';
    try {
      const data = await this.services.endpoints.branch(session.id, {
        anchor_message_id: anchor,
        idempotency_key: operation.idempotencyKey,
      });
      const child =
        data.session && typeof data.session === 'object'
          ? (data.session as Record<string, unknown>)
          : data;
      const id = String(child.id || data.session_id || '').trim();
      if (!id) throw new Error('Branch response did not identify the new conversation path.');

      createdID = id;
      if (this.retryOperation === operation) this.retryOperation = null;
      let target = this.options.findSession(id);
      if (!target) {
        target = this.options.createSession({ ...child, id });
        this.options.prependSession(target);
      }
      if (mode) {
        const created = target;
        const open = () =>
          this.openPath(this.options.findSession(id) || created, session.id, prefill, autoSend);
        void this.refreshCreatedPath(target);
        if (!this.disposed) {
          const { module, progress } = await this.loadProgress();
          if (this.disposed) return true;
          await module.trackNotes(
            progress,
            id,
            target.number || 0,
            mode,
            focus,
            {
              sourceId: session.id,
              anchor,
            },
            open,
            initial && {
              initial,
              step: this.step,
              owns: ownsModal,
              close: finishBranchModal,
              waitForAction: (fn) => {
                this.openEarly = fn;
              },
              bindRetry: (fn) => {
                this.retryModal = fn;
              },
            },
          );
        }
      } else {
        target = await this.refreshCreatedPath(target);
        await this.openPath(target, session.id, prefill, autoSend);
        finishBranchModal();
      }
      return true;
    } catch (error) {
      const detail = errorMessage(error);
      if (createdID) {
        this.services.toast(
          `New path created, but the browser could not finish opening it: ${detail}`,
          'error',
        );
        finishBranchModal();
        return true;
      }
      const message = detail || 'Could not create conversation path.';
      if (ownsModal()) this.step.value = null;
      this.error.value = message;
      this.services.toast(message, 'error');
      return false;
    } finally {
      this.busy.value = false;
    }
  }
}
