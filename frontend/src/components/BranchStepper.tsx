import { NotesDraft, notesPhase } from './NotesDraft';
import '../styles/features/path-notes.css';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import type { BranchStep } from '../stores/branch-progress';
import { subscribeElapsedClock } from '../platform/elapsed-clock';
import { Icon } from './Icon';

function Elapsed({ started }: { started: number }) {
  const [now, setNow] = useState(performance.now());
  useEffect(() => subscribeElapsedClock(() => setNow(performance.now())), []);
  return (
    <small class="branch-step-elapsed" aria-hidden="true">
      {Math.max(0, Math.floor((now - started) / 1000))}s elapsed
    </small>
  );
}

export function BranchStepper({
  step,
  onSkip,
  onOpen,
  onRetry,
}: {
  step: BranchStep;
  onSkip: () => void;
  onOpen: () => void;
  onRetry: () => void;
}) {
  const open = useRef<HTMLButtonElement>(null);
  const retry = useRef<HTMLButtonElement>(null);
  const state = step.job?.state.value;
  const status = state?.status;
  const failed = state?.outcome === 'failed';
  const skipping = state?.skipping || step.action.early === 'skip';
  const phase =
    step.opening != null
      ? 3
      : step.created == null
        ? 0
        : failed || state?.outcome || state?.writingObservedAt != null
          ? 2
          : 1;
  const writing = Date.parse(status?.writing_at || '');
  const reading = Date.parse(status?.started_at || '');
  const steps = [
    {
      label: `Create path${step.child ? ` #${step.child}` : ''}`,
      detail: 'Copies the conversation up to this turn',
      duration: step.created == null ? NaN : step.created - step.started,
    },
    {
      label: `Read ${status?.source_messages == null ? 'later messages' : `${status.source_messages} later messages`}${step.source ? ` in #${step.source}` : ''}`,
      detail:
        status?.source_messages == null
          ? ''
          : [
              status.omitted_messages ? `${status.omitted_messages} omitted` : '',
              `${status.modified_files?.length ?? 0} files modified`,
              `${status.read_files?.length ?? 0} files read`,
            ]
              .filter(Boolean)
              .join(' · '),
      duration: writing - reading,
    },
    {
      label: 'Write path notes',
      detail: status?.model,
      duration: (state?.endedAt ?? NaN) - (state?.writingObservedAt ?? NaN),
    },
    { label: 'Open path', duration: (step.opened ?? NaN) - (step.opening ?? NaN) },
  ];
  useLayoutEffect(() => {
    (failed ? retry : open).current?.focus();
  }, [failed]);
  return (
    <>
      <span class="visually-hidden" aria-live="polite" aria-atomic="true">
        {phase === 0 ? 'Create path' : phase === 3 ? 'Open path' : notesPhase(state)}
      </span>
      <ol class="branch-steps">
        {steps.map(({ label, detail, duration }, index) => {
          const current = index === phase && step.opened == null;
          const done =
            (index < phase || (index === 3 && step.opened != null)) &&
            (index === 0 ||
              index === 3 ||
              (index === 1 && !!status?.writing_at) ||
              (index === 2 && state?.outcome === 'saved'));
          return (
            <li
              key={index}
              class={`branch-step${current ? ' current' : ''}${done ? ' done' : ''}`}
              aria-current={current ? 'step' : undefined}
            >
              <span class="branch-step-icon" aria-hidden="true">
                {done ? (
                  <Icon class="glyph-icon" name="check" />
                ) : current && failed ? (
                  <Icon class="glyph-icon" name="close" />
                ) : (
                  <span class={current ? 'branch-note-spinner' : 'branch-step-dot'} />
                )}
              </span>
              <span>
                {label}
                {detail && <small>{detail}</small>}
                {failed && index === 2 && <small>{state.error}</small>}
              </span>
              {/* Below 0.05s the time rounds to 0.0s, which says nothing. */}
              {Number.isFinite(duration) && duration >= 50 && (
                <span class="branch-step-time" aria-hidden="true">
                  {(duration / 1000).toFixed(1)}s
                </span>
              )}
              {index === 2 && current && !failed && !state?.outcome && (
                <NotesDraft preview={status?.preview} />
              )}
            </li>
          );
        })}
      </ol>
      <div class="branch-step-actions">
        <Elapsed started={step.started} />
        <button
          type="button"
          class="btn"
          ref={retry}
          onClick={failed ? onRetry : onSkip}
          disabled={!failed && (skipping || step.opening != null)}
        >
          {failed ? 'Retry notes' : skipping ? 'Skipping notes…' : 'Skip notes'}
        </button>
        <button
          type="button"
          class="btn"
          ref={open}
          disabled={step.opening != null}
          onClick={onOpen}
        >
          {failed ? 'Open without notes' : 'Open now'}
        </button>
      </div>
    </>
  );
}
