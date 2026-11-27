import { APIError, decodeSSE } from '../api/client';
import { errorMessage } from '../domain/text';
import { initialProjection } from '../domain/response';
import type { ActiveRun, Message, Session } from '../domain/types';
import type { AppStoreServices } from './app-store-services';
import type { SkillStoreHost } from './skill-store';
import { recordValue, uuid } from './store-utils';

export class SkillRunOperations {
  private readonly runAborts = new Map<string, AbortController>();
  private readonly runCursors = new Map<
    string,
    { sessionId: string; eventsURL: string; sequence: number }
  >();

  constructor(
    private readonly services: AppStoreServices,
    private readonly host: SkillStoreHost,
  ) {}

  private skillRunTerminal(status: unknown): boolean {
    return ['complete', 'completed', 'failed', 'cancelled'].includes(
      String(status || '').toLowerCase(),
    );
  }
  private updateSkillRunMessage(
    sessionId: string,
    runId: string,
    patch: Record<string, unknown>,
  ): void {
    this.host.updateSession(sessionId, (session) => {
      const messages = session.messages.map((message) => {
        if (message.role !== 'skill-run' || String(message.runId || '') !== runId) return message;
        const status = String(patch.status || message.status || 'running');
        const output = String(patch.output ?? message.output ?? '');
        const error = String(patch.error ?? message.error ?? '');
        const progress = String(patch.progress ?? message.progress ?? '');
        return {
          ...message,
          ...patch,
          status,
          output,
          error,
          progress,
          content: error || output || progress,
        };
      });
      return { ...session, messages };
    });
  }
  private applySkillRunEvent(runId: string, envelope: Record<string, unknown>): void {
    const cursor = this.runCursors.get(runId);
    if (!cursor) return;
    const sequence = Number(envelope.sequence || envelope.sequence_number) || 0;
    if (sequence && sequence <= cursor.sequence) return;
    if (sequence) cursor.sequence = sequence;
    const type = String(envelope.type || '');
    const data = recordValue(envelope.data) || envelope;
    const patch: Record<string, unknown> = {};
    if (type === 'skill_run.created')
      Object.assign(patch, {
        status: 'running',
        skill: String(data.skill || ''),
        agent: String(data.agent || ''),
        childSessionId: String(data.child_session_id || ''),
      });
    else if (type === 'skill_run.progress')
      patch.progress = String(data.message || data.progress || data.stage || 'Working…');
    else if (type === 'skill_run.completed')
      Object.assign(patch, {
        status: String(data.status || 'completed'),
        output: String(data.output || ''),
        error: String(data.error || ''),
        progress: '',
        childSessionId: String(data.child_session_id || ''),
      });
    else return;
    this.updateSkillRunMessage(cursor.sessionId, runId, patch);
    if (this.skillRunTerminal(patch.status)) {
      this.runAborts.get(runId)?.abort();
      this.runCursors.delete(runId);
      void this.host.refreshSessionMessages(cursor.sessionId);
    }
  }
  private async reconcileSkillRun(runId: string): Promise<void> {
    const cursor = this.runCursors.get(runId);
    if (!cursor) return;
    const snapshot = await this.services.endpoints.skillRun(cursor.sessionId, runId);
    if (this.services.isDisposed || this.runCursors.get(runId) !== cursor) return;
    const events = Array.isArray(snapshot.events) ? snapshot.events : [];
    events.forEach((event) => {
      if (event && typeof event === 'object')
        this.applySkillRunEvent(runId, event as Record<string, unknown>);
    });
    this.updateSkillRunMessage(cursor.sessionId, runId, {
      status: String(snapshot.status || 'running'),
      output: String(snapshot.output || ''),
      error: String(snapshot.error || ''),
      childSessionId: String(snapshot.child_session_id || ''),
    });
    if (this.skillRunTerminal(snapshot.status)) {
      this.runCursors.delete(runId);
      await this.host.refreshSessionMessages(cursor.sessionId);
    }
  }
  private async followSkillRun(runId: string): Promise<void> {
    const cursor = this.runCursors.get(runId);
    if (!cursor || this.runAborts.has(runId)) return;
    const controller = new AbortController();
    this.runAborts.set(runId, controller);
    try {
      const separator = cursor.eventsURL.includes('?') ? '&' : '?';
      const response = await this.services.api.request(
        `${cursor.eventsURL}${separator}after=${encodeURIComponent(cursor.sequence)}`,
        {
          signal: controller.signal,
          headers: { Accept: 'text/event-stream', 'X-Term-LLM-Session-ID': cursor.sessionId },
        },
        { policy: 'stream', retries: 0, timeoutMs: 0, auth: 'session' },
      );
      if (!response.ok || !response.body)
        throw new APIError(
          (await response.text()) || `Skill run stream returned ${response.status}`,
          response.status,
        );
      for await (const frame of decodeSSE(response.body, controller.signal)) {
        let envelope: Record<string, unknown>;
        try {
          envelope = JSON.parse(frame.data) as Record<string, unknown>;
        } catch {
          continue;
        }
        if (!envelope.type && frame.event !== 'message') envelope.type = frame.event;
        this.applySkillRunEvent(runId, envelope);
        if (!this.runCursors.has(runId)) return;
      }
    } catch (error) {
      if (!controller.signal.aborted)
        this.updateSkillRunMessage(cursor.sessionId, runId, {
          progress:
            navigator.onLine === false
              ? 'Offline — run is safe; reconnecting when online'
              : 'Reconnecting…',
          streamError: String(error),
        });
    } finally {
      if (this.runAborts.get(runId) === controller) this.runAborts.delete(runId);
    }
    if (!this.runCursors.has(runId)) return;
    await this.reconcileSkillRun(runId).catch(() => undefined);
    if (this.runCursors.has(runId))
      this.services.schedule(() => void this.followSkillRun(runId), 1_000);
  }
  async invokeSkill(
    session: Session,
    name: string,
    args: string,
    invocation: string,
    isolated: boolean,
    restorePrompt: () => void,
  ): Promise<void> {
    const id = uuid();
    const optimistic: Message = {
      id: `pending_${id}`,
      role: 'user',
      author: this.host.viewer?.value,
      content: invocation,
      clientMessageId: id,
      created: Date.now(),
    };
    this.host.updateSession(session.id, (entry) => ({
      ...entry,
      messages: [...entry.messages, optimistic],
      lastMessageAt: Date.now(),
    }));
    this.host.trackIntent(session.id, {
      id: optimistic.id,
      clientMessageId: id,
      content: invocation,
      created: optimistic.created,
    });
    try {
      const data = await this.services.endpoints.invokeSkill(
        session.id,
        { name, arguments: args, client_message_id: id },
        id,
        !isolated,
      );
      if (this.services.isDisposed) return;
      if (String(data.execution) === 'isolated' && data.run_id) {
        const runId = String(data.run_id);
        const eventsURL = String(
          data.events_url ||
            `/v1/sessions/${encodeURIComponent(session.id)}/skill-runs/${encodeURIComponent(runId)}/events`,
        );
        const message: Message = {
          id: `skill-run-${runId}`,
          role: 'skill-run',
          content: '',
          created: Date.now(),
          runId,
          skill: name,
          status: String(data.status || 'running'),
          childSessionId: String(data.child_session_id || ''),
          eventsURL,
        };
        this.host.updateSession(session.id, (entry) => ({
          ...entry,
          messages: [...entry.messages, message],
        }));
        this.runCursors.set(runId, { sessionId: session.id, eventsURL, sequence: 0 });
        void this.followSkillRun(runId);
      } else {
        const responseId = String(data.response_id || '');
        const epoch = Number(data.run_epoch) || 0;
        if (!responseId || !epoch)
          throw new Error('Skill invocation did not return a response ID and run epoch.');
        const run: ActiveRun = {
          responseId,
          sessionId: session.id,
          epoch,
          status: 'streaming',
          lastSequence: 0,
          startedRev: Number(data.started_rev) || session.transcriptRev || 0,
          startedAt: Number(data.started_at) || Date.now(),
          reconnects: 0,
          requestId: id,
        };
        this.host.setRun(session.id, initialProjection(run));
        this.host.patchSession(session.id, { activeResponseId: responseId });
        void this.host.streamResponse(responseId, session.id, 0);
      }
      this.host.modal.value = '';
      this.services.toast(`Started ${name}.`, 'success');
    } catch (error) {
      this.host.retireIntent(session.id, id);
      restorePrompt();
      this.services.toast(error, 'error');
    }
  }
  async cancelSkill(sessionId: string, runId: string): Promise<void> {
    // Do not claim cancellation is underway until the server accepts it.
    try {
      await this.services.endpoints.cancelSkillRun(sessionId, runId);
    } catch (error) {
      this.services.toast(error, 'error');
      return;
    }
    this.updateSkillRunMessage(sessionId, runId, {
      status: 'cancelling',
      progress: 'Cancelling…',
    });
    try {
      if (this.runCursors.has(runId))
        await this.reconcileSkillRun(runId).catch(() =>
          this.host.refreshSessionMessages(sessionId, true),
        );
      else await this.host.refreshSessionMessages(sessionId, true);
    } catch (error) {
      this.services.toast(
        `Cancellation was accepted, but the transcript could not refresh: ${errorMessage(error)}`,
        'error',
      );
    }
  }

  dispose(): void {
    this.runAborts.forEach((abort) => abort.abort());
    this.runAborts.clear();
    this.runCursors.clear();
  }
}
