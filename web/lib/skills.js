// Labels for the Skills page, and the one sentence a level move gets when
// the skill is gone. The four labels are the states the page may show.
// A builtin, global, or workspace skill is Locked even when a row says
// otherwise: the loop never writes those files. A source the page does not
// know is shown as the server sent it, and it cannot be moved.
import { ApiError, errorText } from './api.js';

export const SKILL_GONE = 'This skill changed since this page loaded. Reload the list to see its current state.';

export const UNTRACKED_REASON = 'The loop did not learn this skill, and it only edits skills it learned.';

const LOCKED_SOURCES = new Set(['builtin', 'global', 'workspace']);

const SOURCE_LABEL = {
  builtin: 'Built in',
  nusashell: 'Learned',
  global: 'Global',
  workspace: 'This workspace',
};

export function sourceLabel(source) {
  if (Object.prototype.hasOwnProperty.call(SOURCE_LABEL, source)) return SOURCE_LABEL[source];
  return source || '';
}

export function skillLabel(row) {
  if (LOCKED_SOURCES.has(row?.source)) return 'Locked';
  if (row?.changed) return 'Edited by hand';
  if (row?.level === 'agent') return 'Loop may propose';
  if (row?.level === 'user') return 'Only you';
  return row?.level || '';
}

// A move is only offered where the server will accept one: a learned skill
// the loop already has a row for, at user or agent.
export function skillMovable(row) {
  return row?.source === 'nusashell' && row?.tracked === true && (row?.level === 'user' || row?.level === 'agent');
}

// Both 404s from a level move — the skill left the catalog, or it has no
// ownership row — are one sentence. Any other failure keeps the server's
// own words, so a 409 still says the file changed.
export function skillMoveFailure(err) {
  if (err instanceof ApiError && err.status === 404) return SKILL_GONE;
  return errorText(err) || 'The move did not go through.';
}

// pending counts every proposal waiting on the owner; pendingStale counts
// those of them the owner can no longer accept. Zero or a missing count
// is no sentence — the page has no proposal action to attach one to.
export function proposalCount(pending, pendingStale) {
  const n = Number(pending);
  if (!Number.isFinite(n) || n <= 0) return '';
  const waiting = n === 1 ? '1 proposal waiting' : `${n} proposals waiting`;
  const stale = Number(pendingStale);
  if (!Number.isFinite(stale) || stale <= 0) return waiting;
  const tail = stale === 1 ? '1 can no longer be accepted' : `${stale} can no longer be accepted`;
  return `${waiting}. ${tail}.`;
}

// The empty choice is the owner's roots. Each project's primary folder is
// one workspace root; two projects on the same folder share one choice.
export function workspaceChoices(projects) {
  const opts = [{ value: '', text: 'Built-in, learned, and global' }];
  const byPath = new Map();
  for (const project of projects || []) {
    const path = project?.folders?.[0];
    if (!path) continue;
    const existing = byPath.get(path);
    if (existing) existing.text = `${existing.text}, ${project.name}`;
    else {
      const opt = { value: path, text: project.name || path };
      byPath.set(path, opt);
      opts.push(opt);
    }
  }
  return opts;
}
