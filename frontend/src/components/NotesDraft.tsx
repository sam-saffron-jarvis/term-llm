import type { NotesState } from '../stores/branch-progress';

export function NotesDraft({ preview }: { preview?: string }) {
  return (
    <div class="path-notes-draft">
      <strong>Draft</strong>
      {preview ? (
        <div class="path-notes-draft-text">{preview}</div>
      ) : (
        <div class="path-notes-skeleton" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      )}
    </div>
  );
}

export function notesPhase(state?: NotesState) {
  if (state?.outcome === 'failed') return 'Notes couldn’t be prepared';
  return state?.status.state === 'writing'
    ? 'Write path notes'
    : state?.status.state === 'saving'
      ? 'Save path notes'
      : 'Read later messages';
}
