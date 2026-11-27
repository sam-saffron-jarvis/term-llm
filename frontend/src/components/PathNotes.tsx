import { useCallback, useId, useState } from 'preact/hooks';
import { rebaseHubAssetURL } from '../app/config';
import { useStore } from '../app/context';
import type { Message } from '../domain/types';
import { Icon, type IconName } from './Icon';
import { NotesDraft, notesPhase } from './NotesDraft';
import { pathSource, treeNodes, type ForkSource } from '../domain/fork';
import '../styles/features/path-notes.css';
import { Markdown } from './Markdown';
import { openMediaGallery } from './transcript-media';

function SourceButton({ source, title = false }: { source: ForkSource; title?: boolean }) {
  const store = useStore();
  if (!source.number) return <>an earlier path</>;
  return (
    <button
      class="text-action path-notes-link"
      type="button"
      onClick={() => void store.resolveAndSelectSession(source.id)}
    >
      {title
        ? `#${source.number}${source.title ? ` ${source.title}` : ''}`
        : `Open #${source.number}`}
    </button>
  );
}

export function ForkDivider({
  source,
  count,
  pending,
  hasNote,
}: {
  source: ForkSource;
  count?: number;
  pending?: boolean;
  hasNote?: boolean;
}) {
  return (
    <div class="fork-divider">
      <span>
        <Icon class="glyph-icon" name="fork" /> Forked from <SourceButton source={source} title />
        {count != null
          ? ` · ${count} later messages there`
          : !hasNote && !pending && ' · clean, nothing carried'}
      </span>
    </div>
  );
}

function Chip({ icon, label, title }: { icon: IconName; label: string; title?: string }) {
  return (
    <span class="path-notes-chip" title={title}>
      <Icon class="glyph-icon" name={icon} />
      {label}
    </span>
  );
}

export function PathNoteCard({ message, latest }: { message: Message; latest: boolean }) {
  const store = useStore();
  const rebase = useCallback(
    (value: string) => rebaseHubAssetURL(store.config, value),
    [store.config],
  );
  const id = useId();
  const [override, setOverride] = useState<{ latest: boolean; expanded: boolean }>();
  const expanded = override?.latest === latest ? override.expanded : latest;
  const note = message.provenance;
  const tree = store.branchTree.value;
  const source = pathSource(
    tree?.active_session_id === store.activeSessionId.value ? treeNodes(tree) : [],
    note?.source_session_id || '',
  );
  const focus = note?.focus;
  return (
    <article class="path-notes-card" aria-label="Path notes" data-message-id={message.id}>
      <button
        class="path-notes-card-head"
        type="button"
        aria-expanded={expanded}
        aria-controls={id}
        onClick={() => setOverride({ latest, expanded: !expanded })}
      >
        <span class="path-notes-glyph">
          <Icon class="glyph-icon" name="file-text" />
        </span>
        <span>
          <strong>Path notes</strong>{' '}
          <small>· from {source.number ? `#${source.number}` : 'an earlier path'}</small>
        </span>
        <Icon
          name={expanded ? 'chevron-down' : 'chevron-right'}
          class="glyph-icon path-notes-chevron"
        />
      </button>
      <div class="path-notes-card-body" id={id} hidden={!expanded}>
        {expanded && (
          <>
            <Markdown
              value={message.content}
              rebase={rebase}
              onMedia={(src, type) => openMediaGallery(store, src, type)}
            />
            {note && (
              <div class="path-notes-card-chips">
                <Chip
                  icon="message-circle-question"
                  label={`${note.source_messages ?? 0} later messages read`}
                />
                {!!note.omitted_messages && (
                  <Chip icon="message-circle-question" label={`${note.omitted_messages} omitted`} />
                )}
                {!!note.modified_files?.length && (
                  <Chip
                    icon="file-pen"
                    label={`${note.modified_files.length} files modified`}
                    title={note.modified_files.join('\n')}
                  />
                )}
                {!!note.read_files?.length && (
                  <Chip
                    icon="file"
                    label={`${note.read_files.length} files read`}
                    title={note.read_files.join('\n')}
                  />
                )}
                {focus && (
                  <Chip
                    icon="target"
                    label={`Focus: “${focus.slice(0, 48)}${focus.length > 48 ? '…' : ''}”`}
                    title={focus}
                  />
                )}
                {note.model && <Chip icon="sparkles" label={note.model} />}
              </div>
            )}
            <div class="path-notes-card-foot path-notes-card-foot-end">
              {!!source.number && <SourceButton source={source} />}
              <button
                class="text-action path-notes-link"
                type="button"
                onClick={() => {
                  store.modal.value = 'inspect';
                }}
              >
                Inspect
              </button>
            </div>
          </>
        )}
      </div>
    </article>
  );
}

export function PathNotesPending({ sessionId }: { sessionId: string }) {
  const store = useStore();
  const branches = store.branchStore;
  const job = branches.notesJobs.value.get(sessionId);
  const state = job?.state.value;
  if (!job || !state || (state.outcome && state.outcome !== 'failed')) return null;
  const failed = state.outcome === 'failed';
  const writing = state.status.state !== 'reading';
  return (
    <section class="path-notes-card path-notes-pending" aria-label="Path notes progress">
      <strong aria-live="polite" aria-atomic="true">
        {!failed && <span class="branch-note-spinner" aria-hidden="true" />} {notesPhase(state)}
      </strong>
      {failed ? (
        <div class="path-notes-card-foot">
          <button
            type="button"
            class="text-action path-notes-link"
            onClick={() => branches.retryNotes(job)}
          >
            Retry
          </button>
          <button
            type="button"
            class="text-action path-notes-link"
            onClick={() => branches.dismissNotes(job)}
          >
            Dismiss
          </button>
        </div>
      ) : (
        <>
          <div class="path-notes-card-chips">
            <span class="path-notes-chip done">Path created</span>
            <span class="path-notes-chip" aria-current={!writing ? 'step' : undefined}>
              Read later messages
            </span>
            <span class="path-notes-chip" aria-current={writing ? 'step' : undefined}>
              Write path notes
            </span>
          </div>
          {writing && <NotesDraft preview={state.status.preview} />}
          <div class="path-notes-card-foot">
            <button
              type="button"
              class="text-action path-notes-link"
              disabled={state.skipping}
              onClick={() => branches.skipNotes(job)}
            >
              {state.skipping ? 'Skipping notes…' : 'Skip notes'}
            </button>
          </div>
        </>
      )}
    </section>
  );
}
