import type { ComponentProps } from 'preact';
import { lazyComponent } from './lazyComponent';

function load<T extends keyof typeof import('./PathNotes')>(name: T) {
  return () => import('./PathNotes').then((module) => module[name]);
}

export const ForkDivider = lazyComponent(() => load('ForkDivider')().catch(() => () => null));
export const PathNotesPending = lazyComponent(() =>
  load('PathNotesPending')().catch(() => () => null),
);
const loadCard = load('PathNoteCard');
export const PathNoteCard = lazyComponent<ComponentProps<Awaited<ReturnType<typeof loadCard>>>>(
  () =>
    loadCard().catch(() => ({ message }) => (
      <article class="path-notes-card" data-message-id={message.id}>
        <strong>Path notes</strong>
        <pre>{message.content}</pre>
      </article>
    )),
);

export function preloadPathNotes() {
  return Promise.all([ForkDivider.preload(), PathNoteCard.preload(), PathNotesPending.preload()]);
}
