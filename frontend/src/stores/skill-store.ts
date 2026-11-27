import { signal, type ReadonlySignal, type Signal } from '@preact/signals';
import type { ResponseProjection } from '../domain/response';
import type { Author, Session } from '../domain/types';
import type { Modal } from './store-types';
import type { AppStoreServices } from './app-store-services';
import { listFrom } from './store-utils';
import type { SkillRunOperations } from './skill-run-operations';

export interface SkillStoreHost {
  viewer?: ReadonlySignal<Author | undefined>;
  activeSession: ReadonlySignal<Session | null>;
  activeSessionId: ReadonlySignal<string>;
  draftActive: ReadonlySignal<boolean>;
  draftStorageId: () => string;
  activeProjectId: ReadonlySignal<string>;
  projectsEnabled: ReadonlySignal<boolean>;
  currentWorktreeDir: ReadonlySignal<string>;
  /** Creates the session for the open draft; resolves '' after reporting a failure. */
  ensureSession: () => Promise<string>;
  streaming: ReadonlySignal<boolean>;
  prompt: Signal<string>;
  modal: Signal<Modal>;
  updateSession: (id: string, updater: (session: Session) => Session) => void;
  setRun: (sessionId: string, run: ResponseProjection) => void;
  patchSession: (id: string, patch: Partial<Session>) => void;
  refreshSessionMessages: (sessionId: string, propagateError?: boolean) => Promise<void>;
  trackIntent: (
    sessionId: string,
    intent: { id: string; clientMessageId: string; content: string; created: number },
  ) => void;
  retireIntent: (sessionId: string, clientMessageId?: string) => void;
  streamResponse: (responseId: string, sessionId: string, sequence: number) => Promise<void>;
}

/** Owns skill discovery and isolated skill-run stream lifecycles. */
export class SkillStore {
  readonly skills = signal<Record<string, unknown>[]>([]);

  readonly cancelling = signal<ReadonlySet<string>>(new Set());

  private operations?: SkillRunOperations;
  private loading?: Promise<SkillRunOperations>;
  private disposed = false;
  private epoch = 0;

  constructor(
    private readonly services: AppStoreServices,
    private readonly host: SkillStoreHost,
  ) {}

  async loadSkills(sessionId = this.host.activeSession.value?.id || ''): Promise<void> {
    const epoch = ++this.epoch;
    if (!sessionId) {
      this.skills.value = [];
      return;
    }
    const data = await this.services.endpoints.skills(sessionId);
    if (epoch !== this.epoch || this.host.activeSessionId.peek() !== sessionId) return;
    this.skills.value = listFrom(data, 'skills', 'items');
  }
  /** Loads skills for an unsent new chat from the workspace it will bind to. */
  async loadDraftSkills(): Promise<void> {
    const epoch = ++this.epoch;
    if (!this.host.draftActive.peek()) return;
    const projectId = this.host.activeProjectId.peek();
    const data = await this.services.endpoints.draftSkills(
      projectId,
      this.host.currentWorktreeDir.peek(),
      this.host.projectsEnabled.peek() && !projectId,
    );
    if (epoch !== this.epoch || !this.host.draftActive.peek()) return;
    this.skills.value = listFrom(data, 'skills', 'items');
  }

  private loadOperations(): Promise<SkillRunOperations> {
    return (this.loading ??= import('./skill-run-operations')
      .then(
        ({ SkillRunOperations }) =>
          (this.operations = new SkillRunOperations(this.services, this.host)),
      )
      .catch((cause: unknown) => {
        this.loading = undefined;
        throw new Error('Could not load skill controls. Reload the page to retry.', { cause });
      }));
  }

  async invokeSkill(name: string, args: string): Promise<void> {
    const invocation = `/${name}${args.trim() ? ` ${args.trim()}` : ''}`;
    const draft = this.host.draftActive.peek() ? this.host.draftStorageId() : null;
    let session = draft === null ? this.host.activeSession.peek() : null;
    const ownerUnchanged = () =>
      draft !== null && !session
        ? this.host.draftActive.peek() && this.host.draftStorageId() === draft
        : !this.host.draftActive.peek() && this.host.activeSession.peek()?.id === session?.id;
    const restorePrompt = () => {
      if (ownerUnchanged() && !this.host.prompt.peek()) this.host.prompt.value = invocation;
    };
    if (draft === null && !session) {
      restorePrompt();
      this.services.toast('Start a conversation before running a skill.', 'error');
      return;
    }
    const isolated =
      this.skills.peek().find((entry) => String(entry.name || '') === name)?.execution ===
      'isolated';
    try {
      const operations = this.operations ?? (await this.loadOperations());
      if (this.disposed) return;
      if (!ownerUnchanged())
        throw new Error('The conversation changed before the skill could start. Try again.');
      if (draft !== null) {
        // Materialize only after the handler is ready; an import failure must not create a chat.
        const id = await this.host.ensureSession();
        if (this.disposed) return;
        if (!id) {
          restorePrompt();
          return;
        }
        session = this.host.activeSession.peek();
        if (this.host.draftActive.peek() || session?.id !== id)
          throw new Error('The conversation changed before the skill could start. Try again.');
      }
      if (!session) throw new Error('Start a conversation before running a skill.');
      if (this.host.streaming.peek() && !isolated) {
        restorePrompt();
        throw new Error('This main-conversation skill cannot run while a response is active.');
      }
      await operations.invokeSkill(session, name, args, invocation, isolated, restorePrompt);
    } catch (error) {
      if (this.disposed) return;
      restorePrompt();
      this.services.toast(error, 'error');
    }
  }

  async cancelSkill(runId: string): Promise<void> {
    const session = this.host.activeSession.value;
    if (!session) {
      this.services.toast('Open this skill’s conversation before cancelling it.', 'error');
      return;
    }
    if (this.cancelling.peek().has(runId)) return;
    this.cancelling.value = new Set([...this.cancelling.peek(), runId]);
    try {
      const operations = this.operations ?? (await this.loadOperations());
      if (!this.disposed) await operations.cancelSkill(session.id, runId);
    } catch (error) {
      if (!this.disposed) this.services.toast(error, 'error');
    } finally {
      const pending = new Set(this.cancelling.peek());
      pending.delete(runId);
      this.cancelling.value = pending;
    }
  }

  dispose(): void {
    this.epoch += 1;
    this.disposed = true;
    this.operations?.dispose();
  }
}
