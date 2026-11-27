import { liveAudioSocketSupported } from '../api/live-socket';

export type VoicePhase =
  | 'idle'
  | 'requesting-permission'
  | 'recording'
  | 'preparing'
  | 'transcribing'
  | 'complete'
  | 'cancelled'
  | 'failed';

export interface VoiceCapability {
  supported: boolean;
  reason: string;
}

export function microphoneCapability(): VoiceCapability {
  if (!globalThis.isSecureContext)
    return { supported: false, reason: 'Voice recording requires a secure HTTPS connection.' };
  if (!navigator.mediaDevices?.getUserMedia)
    return { supported: false, reason: 'This browser cannot access a microphone.' };
  return { supported: true, reason: '' };
}

export type LiveTransport = 'webrtc' | 'websocket_pcm' | 'http_pcm';

// liveCapability lives beside the other media checks so the live store can ask
// it without pulling the lazily loaded call implementation into the eager shell bundle.
export function liveCapability(transport: LiveTransport = 'webrtc'): VoiceCapability {
  const microphone = microphoneCapability();
  if (!microphone.supported)
    return { ...microphone, reason: microphone.reason.replace('Voice recording', 'Live voice') };
  if (transport === 'websocket_pcm' || transport === 'http_pcm') {
    if (
      (transport === 'websocket_pcm' && !liveAudioSocketSupported()) ||
      typeof globalThis.AudioContext !== 'function' ||
      typeof globalThis.AudioWorkletNode !== 'function'
    )
      return {
        supported: false,
        reason: 'This browser cannot stream live PCM audio securely.',
      };
    return { supported: true, reason: '' };
  }
  if (typeof globalThis.RTCPeerConnection !== 'function')
    return { supported: false, reason: 'This browser cannot start a live voice connection.' };
  return { supported: true, reason: '' };
}

export interface VoiceSnapshot {
  phase: VoicePhase;
  capability: VoiceCapability;
  generation: number;
  owner: string;
  ownerRevision: number;
  durationMs: number;
  bytes: number;
  stage?: 'uploading' | 'processing' | 'stalled';
  loaded?: number;
  total?: number;
  elapsedMs?: number;
  transcript?: string;
  error?: string;
  retryable?: boolean;
  retainedBlob?: boolean;
}

export interface VoiceUploadControls {
  signal?: AbortSignal;
  onProgress?: (loaded: number, total?: number) => void;
}

export type VoiceUpload = (
  form: FormData,
  controls: VoiceUploadControls,
) => Promise<Record<string, unknown>>;

export function voiceCapability(): VoiceCapability {
  const microphone = microphoneCapability();
  if (!microphone.supported) return microphone;
  if (!('MediaRecorder' in window))
    return { supported: false, reason: 'This browser cannot record microphone audio.' };
  if (
    typeof MediaRecorder.prototype.start !== 'function' ||
    typeof MediaRecorder.prototype.stop !== 'function'
  )
    return { supported: false, reason: 'This browser has an incomplete audio recorder.' };
  return { supported: true, reason: '' };
}

export function voiceError(error: unknown): { message: string; retryable: boolean } {
  const name = (error as { name?: string } | null)?.name || '';
  if (name === 'NotAllowedError' || name === 'SecurityError')
    return {
      message: 'Microphone access was denied. Enable it in browser or system settings.',
      retryable: true,
    };
  if (name === 'NotFoundError' || name === 'DevicesNotFoundError')
    return { message: 'No microphone was found.', retryable: true };
  if (name === 'AbortError') return { message: 'Recording cancelled.', retryable: false };
  if (name === 'TimeoutError')
    return { message: 'Transcription timed out. Retry the prepared recording.', retryable: true };
  return {
    message:
      error instanceof Error && error.message ? error.message : 'Voice transcription failed.',
    retryable: true,
  };
}

export function idleVoiceSnapshot(generation = 0): VoiceSnapshot {
  return {
    phase: 'idle',
    capability: voiceCapability(),
    generation,
    owner: '',
    ownerRevision: 0,
    durationMs: 0,
    bytes: 0,
  };
}

export const loadVoiceOperation = () => import('./voice-operation');
