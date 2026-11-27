import { describe, expect, it } from 'vitest';
import {
  activeSessionCount,
  buildRegistrationCommand,
  nodeResumePath,
  relativeSessionTime,
  runningSessions,
  sessionMetaText,
  shellQuote,
  waitingLabel,
} from './formatting';
import type { HubNode } from './types';

const node = (sessions: HubNode['sessions']): HubNode =>
  ({ id: 'n', name: 'N', status: { reachable: true }, sessions }) as HubNode;

describe('Hub formatting', () => {
  it('formats relative times and session metadata', () => {
    const now = new Date('2026-09-02T12:00:00Z').getTime();
    expect(relativeSessionTime(now - 10_000, now)).toBe('just now');
    expect(relativeSessionTime(now - 5 * 60_000, now)).toBe('5m ago');
    expect(relativeSessionTime(now - 3 * 3_600_000, now)).toBe('3h ago');
    expect(relativeSessionTime(now - 2 * 86_400_000, now)).toBe('2d ago');
    expect(relativeSessionTime(0, now)).toBe('');
    expect(
      sessionMetaText(
        {
          id: 's',
          short_title: 'Session',
          resume_path: '/resume',
          interaction_required: true,
          pending_interaction_count: 2,
          message_count: 1,
        },
        { now },
      ),
    ).toBe('2 decisions waiting · 1 message');
    expect(
      sessionMetaText(
        { id: 's', short_title: 'Session', resume_path: '/resume', message_count: 3 },
        { unseen: true, now },
      ),
    ).toBe('to review · 3 messages');
  });

  it.each(['active_run', 'active_children'] as const)(
    'formats %s as active without adding a new status',
    (field) => {
      const session = { id: 's', short_title: 'Session', resume_path: '/resume', [field]: true };
      expect(sessionMetaText(session)).toBe('active now');
      expect(sessionMetaText(session, { unseen: true })).toBe('active now');
      expect(sessionMetaText({ ...session, interaction_required: true })).toBe('waiting for input');
    },
  );

  it.each([undefined, 0, -1, 'invalid date'])('ignores invalid session time %s', (value) => {
    expect(relativeSessionTime(value)).toBe('');
  });

  it('accepts ISO session timestamps', () => {
    const now = Date.parse('2026-09-02T12:00:00Z');
    expect(relativeSessionTime('2026-09-02T11:55:00Z', now)).toBe('5m ago');
  });

  it('uses the documented resume fallback and active counts', () => {
    expect(
      nodeResumePath(
        node({ count_label: '', recent: [{ id: 's', short_title: '', resume_path: '/recent' }] }),
      ),
    ).toBe('/recent');
    expect(
      activeSessionCount([
        node({ count_label: '', active_count: 3, active: [] }),
        node({ count_label: '', active: [{ id: 's', short_title: '', resume_path: '/' }] }),
      ]),
    ).toBe(4);
  });

  it('ignores malformed counts and lists in cross-node totals', () => {
    const malformed = node({
      count_label: '',
      active_count: { toString: null },
      active: { length: 100 },
    } as unknown as NonNullable<HubNode['sessions']>);
    expect(activeSessionCount([malformed, node({ count_label: '', active_count: 2 })])).toBe(2);
  });

  it.each([
    [[], 1, 'Question'],
    [['ask_user'], 1, 'Question'],
    [['approval.shell'], 1, 'Shell approval'],
    [['approval.workspace'], 1, 'Workspace access'],
    [['approval.file_write'], 1, 'File write approval'],
    [['approval'], 1, 'Approval'],
    [['approval.future'], 1, 'Approval'],
    [['approval.shell', 'ask_user'], 1, 'Question and approval'],
    [['approval.shell', 'ask_user'], 3, '3 decisions'],
  ])('names waiting kinds %j (count %i) as %s', (kinds, count, label) => {
    expect(waitingLabel(kinds, count)).toBe(label);
  });

  it('lists running sessions in node order, leaving waiting ones and counting unlisted ones', () => {
    const session = (id: string, extra = {}) => ({
      id,
      short_title: id,
      resume_path: `/${id}`,
      active_run: true,
      ...extra,
    });
    const alpha = node({
      count_label: '',
      active_count: 5,
      active: [session('a1'), session('a2', { interaction_required: true })],
    });
    const beta = node({ count_label: '', active: [session('b1')] });
    const idle = node({ count_label: '', active_count: 0 });
    const running = runningSessions([alpha, idle, beta]);
    expect(running.sessions.map(({ session }) => session.id)).toEqual(['a1', 'b1']);
    expect(running.more).toEqual([{ node: alpha, count: 3 }]);
    expect(running.total).toBe(5);
  });

  it('ignores malformed active lists and counts when listing running sessions', () => {
    const malformed = node({
      count_label: '',
      active_count: 'many',
      active: { length: 4 },
    } as unknown as NonNullable<HubNode['sessions']>);
    const sparse = node({
      count_label: '',
      active: [null, { short_title: 'no id' }],
    } as unknown as NonNullable<HubNode['sessions']>);
    const running = runningSessions([malformed, sparse]);
    expect(running.sessions).toEqual([]);
    expect(running.more).toEqual([]);
    expect(running.total).toBe(2);
  });

  it('shell-quotes apostrophes and builds a reverse registration command', () => {
    expect(shellQuote("https://hub.test/o'connor/")).toBe("'https://hub.test/o'\\''connor/'");
    const command = buildRegistrationCommand("https://hub.test/o'connor/");
    expect(command).toContain("export HUB_URL='https://hub.test/o'\\''connor/'");
    expect(command).toContain('--hub-connect reverse');
    expect(command).toContain('--hub-registration-token "$HUB_REGISTRATION_TOKEN"');
  });
});
