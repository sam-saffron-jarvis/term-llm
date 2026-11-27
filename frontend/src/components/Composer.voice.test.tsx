import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StoreContext } from '../app/context';
import { AppStore } from '../stores/app-store';
import { deferred, testConfig, testSession } from '../stores/store-test-fixtures';
import * as voice from '../platform/voice';
import { Composer } from './Composer';

type VoiceModule = Awaited<ReturnType<typeof voice.loadVoiceOperation>>;

let getUserMedia: ReturnType<typeof vi.fn<() => Promise<MediaStream>>>;
let startRecording: ReturnType<typeof vi.fn<(timeslice: number) => void>>;

function pauseMicrophone() {
  const permission = deferred<MediaStream>();
  const track = Object.assign(new EventTarget(), { stop: vi.fn() });
  const stream = { getTracks: () => [track] } as unknown as MediaStream;
  getUserMedia.mockReturnValueOnce(permission.promise);
  return { grant: () => permission.resolve(stream), deny: permission.reject, stop: track.stop };
}

beforeEach(() => {
  localStorage.clear();
  getUserMedia = vi.fn(() => new Promise<MediaStream>(() => {}));
  startRecording = vi.fn();
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal(
    'navigator',
    Object.create(navigator, {
      mediaDevices: { value: { getUserMedia } },
    }),
  );
  vi.stubGlobal(
    'MediaRecorder',
    class extends EventTarget {
      state = 'inactive';
      start(timeslice: number) {
        startRecording(timeslice);
      }
      stop() {}
    },
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function setup() {
  const store = new AppStore(testConfig);
  store.sessions.value = [testSession(), testSession({ id: 's2' })];
  store.activeSessionId.value = 's1';
  store.draftActive.value = false;
  store.startupDone.value = true;
  const view = render(
    <StoreContext.Provider value={store}>
      <Composer />
    </StoreContext.Provider>,
  );
  return { store, view, mic: screen.getByRole('button', { name: 'Record voice message' }) };
}

function pauseVoiceLoading() {
  const gate = deferred<VoiceModule>();
  const load = vi.spyOn(voice, 'loadVoiceOperation').mockReturnValue(gate.promise);
  return { ...gate, load };
}

describe('deferred dictation', () => {
  it('requests the microphone during the cold click and records once the chunk arrives', async () => {
    const gate = pauseVoiceLoading();
    const microphone = pauseMicrophone();
    const { store, view, mic } = setup();
    expect(mic).toBeEnabled();
    expect(gate.load).not.toHaveBeenCalled();
    expect(screen.queryByRole('group', { name: 'Voice dictation' })).toBeNull();
    fireEvent.click(mic);
    expect(mic).toBeDisabled();
    expect(screen.getByRole('group', { name: 'Voice dictation' })).toHaveTextContent(
      'Requesting microphone access…',
    );
    expect(getUserMedia).toHaveBeenCalledExactlyOnceWith({ audio: true });
    await act(() => microphone.grant());
    expect(startRecording).not.toHaveBeenCalled();
    await act(async () => gate.resolve(await import('../platform/voice-operation')));
    await waitFor(() => expect(startRecording).toHaveBeenCalledExactlyOnceWith(500));
    expect(microphone.stop).not.toHaveBeenCalled();
    expect(await screen.findByText('Recording 0:00')).toBeVisible();
    await act(() => view.unmount());
    expect(microphone.stop).toHaveBeenCalledOnce();
    store.dispose();
  });

  it('shares a focus preload with keyboard activation without requesting the mic early', async () => {
    const gate = pauseVoiceLoading();
    const microphone = pauseMicrophone();
    const { store, view, mic } = setup();
    fireEvent.focus(mic);
    expect(gate.load).toHaveBeenCalledOnce();
    expect(mic).toBeEnabled();
    expect(getUserMedia).not.toHaveBeenCalled();
    // Native keyboard activation dispatches the button's click.
    fireEvent.click(mic, { detail: 0 });
    expect(gate.load).toHaveBeenCalledOnce();
    expect(mic).toBeDisabled();
    expect(getUserMedia).toHaveBeenCalledExactlyOnceWith({ audio: true });
    await act(async () => gate.resolve(await import('../platform/voice-operation')));
    expect(startRecording).not.toHaveBeenCalled();
    await act(() => microphone.grant());
    await waitFor(() => expect(startRecording).toHaveBeenCalledOnce());
    expect(getUserMedia).toHaveBeenCalledOnce();
    await act(() => view.unmount());
    expect(microphone.stop).toHaveBeenCalledOnce();
    store.dispose();
  });

  it.each([
    ['cancel', true],
    ['cancel', false],
    ['switch conversation', false],
    ['unmount', true],
  ] as const)(
    'releases the microphone on %s during import (permission already granted: %s)',
    async (action, granted) => {
      const gate = pauseVoiceLoading();
      const microphone = pauseMicrophone();
      const { store, view, mic } = setup();
      fireEvent.click(mic);
      expect(getUserMedia).toHaveBeenCalledOnce();
      if (granted) await act(() => microphone.grant());
      if (action === 'cancel') fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      else if (action === 'unmount') await act(() => view.unmount());
      else
        await act(() => {
          store.activeSessionId.value = 's2';
        });
      if (!granted) await act(() => microphone.grant());
      expect(microphone.stop).toHaveBeenCalledOnce();
      await act(async () => gate.resolve(await import('../platform/voice-operation')));
      expect(startRecording).not.toHaveBeenCalled();
      await act(() => view.unmount());
      expect(microphone.stop).toHaveBeenCalledOnce();
      store.dispose();
    },
  );

  it.each([true, false])(
    'releases the microphone on import failure and requests it in Retry (permission already granted: %s)',
    async (granted) => {
      const gate = pauseVoiceLoading();
      const microphone = pauseMicrophone();
      const { store, view, mic } = setup();
      fireEvent.click(mic);
      if (granted) await act(() => microphone.grant());
      await act(() => gate.reject(new Error('chunk not found')));
      expect(await screen.findByRole('alert')).toHaveTextContent('Could not load voice dictation');
      if (!granted) await act(() => microphone.grant());
      expect(microphone.stop).toHaveBeenCalledOnce();
      expect(startRecording).not.toHaveBeenCalled();
      gate.load.mockResolvedValue(await import('../platform/voice-operation'));
      const retry = pauseMicrophone();
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      expect(getUserMedia).toHaveBeenCalledTimes(2);
      await act(() => retry.grant());
      await waitFor(() => expect(startRecording).toHaveBeenCalledOnce());
      await act(() => view.unmount());
      expect(retry.stop).toHaveBeenCalledOnce();
      store.dispose();
    },
  );

  it('announces permission denial without waiting for the import', async () => {
    const gate = pauseVoiceLoading();
    const microphone = pauseMicrophone();
    const { store, view, mic } = setup();
    fireEvent.click(mic);
    await act(() => microphone.deny(new DOMException('Access denied', 'NotAllowedError')));
    expect(await screen.findByRole('alert')).toHaveTextContent('Microphone access was denied');
    await act(async () => gate.resolve(await import('../platform/voice-operation')));
    expect(startRecording).not.toHaveBeenCalled();
    await act(() => view.unmount());
    store.dispose();
  });

  it('keeps the live-call stop control available when dictation is busy', async () => {
    pauseVoiceLoading();
    const { store, view, mic } = setup();
    await act(() => {
      store.liveStore.enabled.value = true;
      store.liveStore.capability.value = { supported: true, reason: '' };
    });
    fireEvent.click(mic);
    expect(screen.getByRole('button', { name: 'Start live voice' })).toBeDisabled();
    // A live call can be started by another surface while dictation is pending.
    await act(() => {
      store.liveStore.liveId.value = 'live-one';
      store.liveStore.phase.value = 'listening';
    });
    expect(screen.getByRole('button', { name: 'Stop live voice' })).toBeEnabled();
    await act(() => {
      store.prompt.value = '/thinking expanded';
    });
    expect(screen.getByRole('button', { name: 'Send text to live voice' })).toBeDisabled();
    await act(() => view.unmount());
    store.dispose();
  });
});
