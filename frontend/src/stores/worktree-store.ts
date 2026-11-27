import { computed, signal, type ReadonlySignal, type Signal } from '@preact/signals';
import { errorMessage } from '../domain/text';
import type { Project, Session } from '../domain/types';
import { readDrafts, saveDraft } from '../platform/storage';
import type { Modal, SendOptions } from './store-types';
import type { WorktreeOperations } from './worktree-operations';
import type { AppStoreServices } from './app-store-services';

export interface WorktreeStoreOptions {
  projectsEnabled: ReadonlySignal<boolean>;
  worktreesEnabled: ReadonlySignal<boolean>;
  activeProjectId: ReadonlySignal<string>;
  activeSession: ReadonlySignal<Session | null>;
  draftActive: ReadonlySignal<boolean>;
  prompt: ReadonlySignal<string>;
  projects: ReadonlySignal<Project[]>;
  modal: Signal<Modal>;
  selectedDraftWorktree: Signal<string>;
  draftStorageId: () => string;
  patchSession: (id: string, patch: Partial<Session>) => void;
  send: (options?: SendOptions) => Promise<void>;
}

/** Owns worktree discovery and worktree commands for the active project. */
export class WorktreeStore {
  readonly worktrees = signal<Record<string, unknown>[]>([]);
  readonly error = signal('');
  readonly currentDir: ReadonlySignal<string>;
  private operations?: WorktreeOperations;
  private loading?: Promise<WorktreeOperations>;

  constructor(
    private readonly services: AppStoreServices,
    private readonly options: WorktreeStoreOptions,
  ) {
    // A root checkout is represented by an empty path. Select by conversation
    // mode rather than truthiness so root can never fall through to draft-only
    // state left over from another composer.
    this.currentDir = computed(() =>
      this.options.draftActive.value
        ? this.options.selectedDraftWorktree.value
        : this.options.activeSession.value?.worktreeDir || '',
    );
  }

  available(): boolean {
    if (!this.options.projectsEnabled.value) return this.options.worktreesEnabled.value;
    const projectId =
      this.options.activeSession.value?.projectId || this.options.activeProjectId.value;
    const project = this.options.projects.value.find((entry) => entry.id === projectId);
    return Boolean(project?.git && project.available !== false && !project.archived);
  }

  private setDraftSelection(dir: string, close: boolean): void {
    this.options.selectedDraftWorktree.value = dir;
    const id = this.options.draftStorageId();
    const draft = readDrafts(this.services.storage, this.services.keys.draftMessages).find(
      (entry) => entry.sessionId === id,
    );
    saveDraft(this.services.storage, this.services.keys.draftMessages, {
      ...(draft || {
        sessionId: id,
        content: this.options.prompt.value,
        updated: Date.now(),
      }),
      worktreeDir: dir,
      projectId: this.options.activeProjectId.value,
    });
    if (close) this.options.modal.value = '';
  }

  chooseDraft(dir: string): void {
    if (!this.options.draftActive.value) return;
    this.setDraftSelection(dir, true);
  }

  private loadOperations(): Promise<WorktreeOperations> {
    return (this.loading ??= import('./worktree-operations')
      .then(
        ({ WorktreeOperations }) =>
          (this.operations = new WorktreeOperations(
            this.services,
            this.options,
            this,
            (dir, close) => this.setDraftSelection(dir, close),
          )),
      )
      .catch((cause: unknown) => {
        this.loading = undefined;
        throw new Error('Could not load worktree controls. Reload the page to retry.', { cause });
      }));
  }

  private projectScope(): string | null {
    return this.options.projectsEnabled.peek()
      ? this.options.activeSession.peek()?.projectId || this.options.activeProjectId.peek()
      : null;
  }

  private async mutate<T>(action: (operations: WorktreeOperations) => Promise<T>): Promise<T> {
    if (this.operations) return action(this.operations);
    const project = this.projectScope();
    const session = this.options.activeSession.peek()?.id;
    const draft = this.options.draftActive.peek() ? this.options.draftStorageId() : null;
    const operations = await this.loadOperations();
    // Never apply a gesture from one workspace to whichever is open after import.
    if (
      this.services.isDisposed ||
      project !== this.projectScope() ||
      session !== this.options.activeSession.peek()?.id ||
      draft !== (this.options.draftActive.peek() ? this.options.draftStorageId() : null)
    )
      throw new Error('The workspace changed before worktree controls were ready. Try again.');
    return action(operations);
  }

  async load(): Promise<void> {
    const project = this.projectScope();
    this.error.value = '';
    try {
      const operations = this.operations ?? (await this.loadOperations());
      if (!this.services.isDisposed && project === this.projectScope()) await operations.load();
    } catch (error) {
      if (this.services.isDisposed || project !== this.projectScope()) return;
      this.worktrees.value = [];
      this.error.value = errorMessage(error);
    }
  }

  create(name: string, clean = false): Promise<void> {
    return this.mutate((operations) => operations.create(name, clean));
  }

  switchTo(dir: string): Promise<void> {
    return this.mutate((operations) => operations.switchTo(dir));
  }

  merge(dir: string, force = false): Promise<Record<string, unknown>> {
    return this.mutate((operations) => operations.merge(dir, force));
  }

  recover(dir: string): Promise<Record<string, unknown>> {
    return this.mutate((operations) => operations.recover(dir));
  }

  promote(dir: string, branch: string): Promise<Record<string, unknown>> {
    return this.mutate((operations) => operations.promote(dir, branch));
  }

  remove(dir: string, force = false): Promise<Record<string, unknown>> {
    return this.mutate((operations) => operations.remove(dir, force));
  }
}
