import { signal, type ReadonlySignal } from '@preact/signals';
import type {
  MCPAddRequest,
  MCPAddResult,
  MCPCatalogueResponse,
  MCPOAuthFlow,
  MCPServer,
  Session,
} from '../domain/types';
import { copyText } from '../platform/clipboard';
import type { AppStoreServices } from './app-store-services';
import type { MCPOperations } from './mcp-operations';

export interface MCPOAuthUIState {
  flowId: string;
  authorizationURL: string;
  state: MCPOAuthFlow['state'];
  error: string;
  popupBlocked: boolean;
}

export interface MCPState {
  ownerId: string;
  servers: MCPServer[];
  enabled: string[];
  loading: boolean;
  pending: string;
  error: string;
  oauth: Record<string, MCPOAuthUIState>;
}

/** A server just removed from mcp.json, kept so the removal can be undone. */
export interface MCPRemovedServer {
  name: string;
  config: Record<string, unknown>;
  wasEnabled: boolean;
}

export interface MCPStoreOptions {
  linked?: () => boolean;
  activeSession: ReadonlySignal<Session | null>;
  draftSessionId: () => string;
  patchSession: (id: string, patch: Partial<Session>) => void;
}

/** Eager session projection and gesture-sensitive controls; network operations load on demand. */
export class MCPStore {
  readonly state = signal<MCPState>({
    ownerId: '',
    servers: [],
    enabled: [],
    loading: false,
    pending: '',
    error: '',
    oauth: {},
  });
  readonly removed = signal<MCPRemovedServer | null>(null);

  private operations?: MCPOperations;
  private loading?: Promise<MCPOperations>;
  private readonly pendingSelections = new Map<string, Promise<boolean>>();

  constructor(
    private readonly services: AppStoreServices,
    private readonly options: MCPStoreOptions,
  ) {}

  private sessionId(): string {
    return this.options.activeSession.value?.id || this.options.draftSessionId();
  }

  enabledFor(ownerId: string): string[] {
    return this.state.value.ownerId === ownerId ? [...this.state.value.enabled] : [];
  }

  rekey(oldId: string, newId: string): void {
    if (!oldId || !newId || this.state.value.ownerId !== oldId) return;
    this.state.value = { ...this.state.value, ownerId: newId };
    this.options.patchSession(newId, { mcpEnabled: this.state.value.enabled });
    this.operations?.rekey(oldId, newId);
  }

  /** Only an in-flight selection edit can delay a send; ordinary sends never import MCP. */
  pendingSelection(ownerId: string): Promise<boolean> | undefined {
    return this.pendingSelections.get(ownerId);
  }

  private loadOperations(): Promise<MCPOperations> {
    return (this.loading ??= import('./mcp-operations')
      .then(
        ({ MCPOperations }) =>
          (this.operations = new MCPOperations(
            this.services,
            this.options,
            this.state,
            this.removed,
          )),
      )
      .catch((cause: unknown) => {
        this.loading = undefined;
        throw new Error('Could not load MCP controls. Reload the page to retry.', { cause });
      }));
  }

  private async withOperations<T>(action: (operations: MCPOperations) => Promise<T>): Promise<T> {
    return action(this.operations ?? (await this.loadOperations()));
  }

  private editSelection<T>(action: (operations: MCPOperations) => Promise<T>): Promise<T> {
    const ownerId = this.sessionId();
    const previous = this.pendingSelections.get(ownerId);
    const edit = this.withOperations((operations) => {
      if (this.services.isDisposed || this.sessionId() !== ownerId)
        throw new Error('The conversation changed before MCP controls were ready. Try again.');
      return action(operations);
    });
    const pending = Promise.allSettled([previous, edit]).then(([earlier, current]) => {
      if (this.pendingSelections.get(ownerId) === pending) this.pendingSelections.delete(ownerId);
      return (
        earlier.status === 'fulfilled' &&
        earlier.value !== false &&
        current.status === 'fulfilled' &&
        current.value !== false
      );
    });
    this.pendingSelections.set(ownerId, pending);
    return edit;
  }

  private report(error: unknown): void {
    this.services.toast(error, 'error');
  }

  async load(): Promise<void> {
    const ownerId = this.sessionId();
    if (!ownerId || this.services.isDisposed) return;
    if (!this.operations) this.state.value = { ...this.state.value, loading: true, error: '' };
    try {
      await this.withOperations(async (operations) => {
        if (!this.services.isDisposed && this.sessionId() === ownerId) await operations.load();
        else if (this.state.value.ownerId !== this.sessionId())
          this.state.value = { ...this.state.value, loading: false };
      });
    } catch (error) {
      this.state.value = { ...this.state.value, loading: false, error: (error as Error).message };
    }
  }

  async toggle(name: string): Promise<void> {
    await this.editSelection((operations) => operations.toggle(name)).catch((error: unknown) =>
      this.report(error),
    );
  }

  searchCatalogue(query: string, signal?: AbortSignal): Promise<MCPCatalogueResponse> {
    return this.withOperations((operations) => operations.searchCatalogue(query, signal));
  }

  preview(request: MCPAddRequest): Promise<MCPAddResult> {
    return this.withOperations((operations) => operations.preview(request));
  }

  addServer(request: MCPAddRequest, enable = true): Promise<MCPAddResult> {
    return this.editSelection((operations) => operations.addServer(request, enable));
  }

  removeServer(name: string): Promise<boolean> {
    return this.editSelection((operations) => operations.removeServer(name)).catch(
      (error: unknown) => {
        this.report(error);
        return false;
      },
    );
  }

  async undoRemove(): Promise<void> {
    await this.editSelection((operations) => operations.undoRemove()).catch((error: unknown) =>
      this.report(error),
    );
  }

  dismissRemoved(): void {
    this.removed.value = null;
  }

  async startOAuth(name: string, force = false): Promise<void> {
    const sessionId = this.sessionId();
    const existing = this.state.value.oauth?.[name];
    if (!sessionId || existing?.state === 'starting' || existing?.state === 'pending') return;
    // Opening a popup must use the original click, even when operations are cold.
    const popup = this.options.linked?.()
      ? null
      : window.open('', '_blank', 'popup=yes,width=560,height=720');
    if (this.operations) return this.operations.startOAuth(sessionId, name, force, popup);

    const starting: MCPOAuthUIState = {
      flowId: '',
      authorizationURL: '',
      state: 'starting',
      error: '',
      popupBlocked: !popup,
    };
    this.state.value = {
      ...this.state.value,
      oauth: { ...this.state.value.oauth, [name]: starting },
    };
    try {
      const operations = await this.loadOperations();
      if (this.services.isDisposed || sessionId !== this.sessionId())
        throw new Error('The conversation changed before authorization could start. Try again.');
      await operations.startOAuth(sessionId, name, force, popup);
    } catch (error) {
      popup?.close();
      if (this.state.value.oauth?.[name] === starting) {
        const oauth = { ...this.state.value.oauth };
        delete oauth[name];
        this.state.value = { ...this.state.value, oauth };
      }
      this.report(error);
    }
  }

  cancelOAuth(name: string): Promise<void> {
    // An actual flow ID is only created after the operations have loaded.
    return this.operations?.cancelOAuth(name) ?? Promise.resolve();
  }

  async logoutOAuth(name: string): Promise<void> {
    await this.editSelection((operations) => operations.logoutOAuth(name)).catch((error: unknown) =>
      this.report(error),
    );
  }

  async copyOAuthLink(name: string): Promise<void> {
    const url = this.state.value.oauth?.[name]?.authorizationURL;
    try {
      if (!url) throw new Error('Authorization link is unavailable. Start authorization again.');
      await copyText(url);
    } catch (error) {
      this.report(error);
    }
  }
}
