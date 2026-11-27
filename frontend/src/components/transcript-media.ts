import type { AppStore } from '../stores/app-store';
import { showAudioMediaError } from '../domain/audio-media';

const pauseTranscriptPlayers = new Set<() => void>();

/** Stop transcript playback before starting a video preview outside its root. */
export function pauseTranscriptMedia(): void {
  for (const pause of pauseTranscriptPlayers) pause();
}

/** Native play/error do not bubble. Keep playback scoped to one transcript. */
export function bindTranscriptMedia(root: HTMLElement): () => void {
  const players = () => [...root.querySelectorAll<HTMLMediaElement>('audio,video')];
  let active: HTMLMediaElement | null = null;
  const pause = (player: HTMLMediaElement) => {
    if (!player.paused) player.pause();
  };
  const onPlay = (event: Event) => {
    const player = event.target;
    if (!(player instanceof HTMLMediaElement)) return;
    if (active && active !== player) pause(active);
    active = player;
    for (const other of players()) if (other !== player) pause(other);
  };
  const onError = (event: Event) => {
    if (event.target instanceof HTMLAudioElement) showAudioMediaError(event.target);
  };
  const observer = new MutationObserver(() => {
    if (active && !root.contains(active)) {
      pause(active);
      active = null;
    }
  });
  const pauseAll = () => {
    for (const player of players()) if (player !== active) pause(player);
    if (active) pause(active);
    active = null;
  };
  pauseTranscriptPlayers.add(pauseAll);
  root.addEventListener('play', onPlay, true);
  root.addEventListener('error', onError, true);
  observer.observe(root, { childList: true, subtree: true });
  return () => {
    pauseTranscriptPlayers.delete(pauseAll);
    root.removeEventListener('play', onPlay, true);
    root.removeEventListener('error', onError, true);
    observer.disconnect();
    pauseAll();
  };
}

export function openMediaGallery(store: AppStore, src: string, type: 'image' | 'video'): void {
  const nodes = [...document.querySelectorAll<HTMLElement>('[data-lightbox-src]')];
  const items = nodes
    .map((node, index) => ({
      key: node.dataset.lightboxKey || `${node.dataset.lightboxSrc}-${index}`,
      src: node.dataset.lightboxSrc || '',
      type: node.dataset.lightboxType === 'video' ? ('video' as const) : ('image' as const),
      name: node.dataset.lightboxName || '',
    }))
    .filter((item) => item.src);
  let index = items.findIndex((item) => item.src === src && item.type === type);
  if (index < 0) {
    items.push({ key: `${src}-${items.length}`, src, type, name: '' });
    index = items.length - 1;
  }
  store.lightbox.value = { ...items[index], items, index };
}
