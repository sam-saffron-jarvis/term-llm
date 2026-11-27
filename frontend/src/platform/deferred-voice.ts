import {
  idleVoiceSnapshot,
  loadVoiceOperation,
  voiceError,
  type VoiceSnapshot,
  type VoiceUpload,
} from './voice';
import type { VoiceOperation } from './voice-operation';

/** Keeps composer controls usable before the recorder chunk is available. */
export class DeferredVoiceOperation {
  snapshot = idleVoiceSnapshot();
  private listener: (snapshot: VoiceSnapshot) => void = () => {};
  private operation?: VoiceOperation;
  private loading?: Promise<typeof VoiceOperation>;
  private starting?: { stream?: MediaStream };
  private disposed = false;

  constructor(
    private readonly upload: VoiceUpload,
    private readonly owns: (owner: string) => boolean,
  ) {}

  subscribe(listener: (snapshot: VoiceSnapshot) => void): () => void {
    this.listener = listener;
    listener(this.snapshot);
    return () => {
      this.listener = () => {};
    };
  }

  private publish = (snapshot: VoiceSnapshot): void => {
    this.snapshot = snapshot;
    this.listener(snapshot);
  };

  private load(): Promise<typeof VoiceOperation> {
    return (this.loading ??= loadVoiceOperation().then(
      ({ VoiceOperation }) => VoiceOperation,
      (error) => {
        this.loading = undefined;
        throw error;
      },
    ));
  }

  preload(): void {
    if (!this.loading && this.snapshot.capability.supported) {
      // Speculative fetches don't interrupt the user; start() reports failures.
      void this.load().catch(() => {});
    }
  }

  async start(owner: string, ownerRevision: number): Promise<void> {
    if (this.disposed || !this.owns(owner)) return;
    if (this.operation) return this.operation.start(owner, ownerRevision);
    if (!['idle', 'failed'].includes(this.snapshot.phase) || !this.snapshot.capability.supported)
      return;
    const attempt: { stream?: MediaStream } = {};
    this.starting = attempt;
    this.publish({
      ...this.snapshot,
      error: undefined,
      retryable: undefined,
      phase: 'requesting-permission',
      owner,
      ownerRevision,
    });
    let Recorder: typeof VoiceOperation;
    try {
      // WebKit uses the current gesture for microphone permission decisions.
      // Ask in the click/Retry handler, not after a potentially slow import.
      const microphone = navigator.mediaDevices.getUserMedia({ audio: true }).then((stream) => {
        if (this.starting === attempt && this.owns(owner)) attempt.stream = stream;
        else stream.getTracks().forEach((track) => track.stop());
      });
      [Recorder] = await Promise.all([
        this.load().catch(() => {
          throw new Error(
            'Could not load voice dictation. Reload the page if retrying does not help.',
          );
        }),
        microphone,
      ]);
    } catch (error) {
      if (this.starting !== attempt) return;
      if (!this.owns(owner)) {
        this.cancel();
        return;
      }
      this.releaseStarting();
      const failure = voiceError(error);
      this.publish({
        ...this.snapshot,
        phase: 'failed',
        error: failure.message,
        retryable: failure.retryable,
      });
      return;
    }
    if (this.starting !== attempt) return;
    if (!this.owns(owner) || !attempt.stream) {
      this.cancel();
      return;
    }
    this.starting = undefined;
    this.operation = new Recorder(this.upload);
    // The loaded operation owns the stream from here. Subscribe after start()
    // so the recorder's initial idle snapshot never replaces the busy strip.
    const recording = this.operation.start(owner, ownerRevision, attempt.stream);
    this.operation.subscribe(this.publish);
    return recording;
  }

  private releaseStarting(): void {
    const stream = this.starting?.stream;
    this.starting = undefined;
    stream?.getTracks().forEach((track) => track.stop());
  }

  stop(): void {
    this.operation?.stop();
  }

  retry(): void {
    if (this.operation) this.operation.retry();
    else void this.start(this.snapshot.owner, this.snapshot.ownerRevision);
  }

  cancel(): void {
    if (this.operation) this.operation.cancel();
    else if (this.starting || this.snapshot.phase === 'failed') {
      this.releaseStarting();
      this.publish({ ...this.snapshot, phase: 'cancelled', error: undefined });
    }
  }

  discard(): void {
    this.cancel();
  }

  settle(): void {
    if (this.operation) this.operation.settle();
    else if (this.snapshot.phase === 'cancelled') this.publish(idleVoiceSnapshot());
  }

  dispose(): void {
    this.disposed = true;
    this.releaseStarting();
    this.operation?.dispose();
    this.listener = () => {};
  }
}
