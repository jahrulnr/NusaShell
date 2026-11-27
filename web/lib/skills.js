// Labels for the Skills page, and the one sentence a level move gets when
// the skill is gone. A non-empty changedCause is the row's label, on every
// source. The words are the field. An empty cause is not a label. A
// builtin, global, or workspace skill is then Locked: the loop never writes
// those files. A source the page does not know is shown as the server sent
// it, and it cannot be moved.
import { ApiError, api, errorText } from './api.js';

export const SKILL_GONE = 'This skill changed since this page loaded. Reload the list to see its current state.';

export const UNTRACKED_REASON = 'The loop did not learn this skill, and it only edits skills it learned.';

// A 404 "no such route" means this server never mounted the skills page.
// Any other failure, including a 404 with different words, means the page
// is there and the read failed.
export function skillRouteMissing(err) {
  return err instanceof ApiError && err.status === 404 && err.message === 'no such route';
}

// unknown until the first read; missing hides the section; offered keeps it.
let skillsRoute = 'unknown';
let skillsRouteGen = 0;
const skillsRouteWatchers = new Set();

export function skillsRouteState() { return skillsRoute; }

export function watchSkillsRoute(fn) {
  skillsRouteWatchers.add(fn);
  return () => skillsRouteWatchers.delete(fn);
}

function publishSkillsRoute(next) {
  if (skillsRoute === next) return;
  skillsRoute = next;
  for (const fn of skillsRouteWatchers) fn(skillsRoute);
}

export function noteSkillsRoute(err) {
  publishSkillsRoute(skillRouteMissing(err) ? 'missing' : 'offered');
}

// One probe for the settings nav and the page. A later reset (tests) drops
// an answer that belongs to the probe it cancelled.
export function ensureSkillsRoute() {
  if (skillsRoute !== 'unknown') return;
  const gen = skillsRouteGen;
  if (ensureSkillsRoute.flight === gen) return;
  ensureSkillsRoute.flight = gen;
  api.skills().then(
    () => { if (gen === skillsRouteGen) noteSkillsRoute(); },
    (err) => { if (gen === skillsRouteGen) noteSkillsRoute(err); },
  );
}
ensureSkillsRoute.flight = -1;

export function resetSkillsRoute() {
  skillsRouteGen += 1;
  ensureSkillsRoute.flight = -1;
  publishSkillsRoute('unknown');
}

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
  // changedCause is the label wherever it is set. The field is the sentence.
  // An empty cause is not a label.
  const cause = typeof row?.changedCause === 'string' ? row.changedCause : '';
  if (cause.trim()) return cause;
  if (LOCKED_SOURCES.has(row?.source)) return 'Locked';
  if (row?.level === 'agent') return 'Loop may propose';
  if (row?.level === 'user') return 'Only you';
  return row?.level || '';
}

// A move is only offered where the server will accept one: a learned skill
// the loop already has a row for, at user or agent.
export function skillMovable(row) {
  return row?.source === 'nusashell' && row?.tracked === true && (row?.level === 'user' || row?.level === 'agent');
}

// A level move that got no answer — the connection dropped, or the server
// took too long — has an unknown outcome: the server may have written the
// change before the answer was lost. It is not a failure, and the sentence
// says so; reloading the list is how the owner sees what the server holds.
export const MOVE_UNKNOWN = 'The result is not known: the server did not answer, so the change may or may not have been saved. Reload the list to see the current level of this skill.';

export function skillMoveUnknown(err) {
  if (!(err instanceof ApiError)) return false;
  if (err.status === 0) return true;
  // A gateway's 502 or 504 may have passed the move on before it failed, so the
  // server may have written. Our own problem document is a definite answer.
  return (err.status === 502 || err.status === 504) && !err.problem;
}

// Both 404s from a level move — the skill left the catalog, or it has no
// ownership row — are one sentence. Any other failure keeps the server's
// own words, so a 409 still says the file changed.
export function skillMoveFailure(err) {
  if (skillMoveUnknown(err)) return MOVE_UNKNOWN;
  if (err instanceof ApiError && err.status === 404) return SKILL_GONE;
  return errorText(err) || 'The move did not go through.';
}

// pending counts every proposal waiting on the owner; pendingStale counts
// those of them the owner can no longer accept. Zero or a missing count
// is no sentence.
export function proposalCount(pending, pendingStale) {
  const n = Number(pending);
  if (!Number.isFinite(n) || n <= 0) return '';
  const waiting = n === 1 ? '1 proposal waiting' : `${n} proposals waiting`;
  const stale = Number(pendingStale);
  if (!Number.isFinite(stale) || stale <= 0) return waiting;
  const tail = stale === 1 ? '1 can no longer be accepted' : `${stale} can no longer be accepted`;
  return `${waiting}. ${tail}.`;
}

// The queue's own sentences. Each one is the line the matrix names; the page
// does not write a second version of any of them.
export const QUEUE_EMPTY = 'Nothing waiting.';
export const QUEUE_PAUSED = 'The loop pauses new proposals while three are open.';
export const QUEUE_LOAD_FAILED = 'Could not load proposals.';
export const QUEUE_STILL_READING = 'Still reading the skill folders.';
export const DISMISS_READS = 'The loop reads this before it proposes again.';
export const DISMISS_CONFIRM_UNKNOWN = 'We could not confirm this. Reloading to check.';
export const DISMISS_NOT_YET = 'Not dismissed yet.';
export const DISMISS_GONE = 'This proposal no longer exists.';
export const DISMISS_SUPERSEDED = "Reasons cannot start with 'superseded:'. Reword and try again.";
export const DISMISS_NOT_RECORDED = 'Not dismissed. Try again.';
export const DISMISS_STILL_SAVING = 'Still saving.';
export const DISMISS_STILL_WAITING = 'Still waiting for the server. The change may already be applied.';
export const DISMISS_PATCH = 'Dismissing leaves the file as it is; set its level on this page afterwards.';
export const LEFTOVER_ROW = 'The skill file is already on disk from an accept that did not finish. Dismissing archives it.';
export const ALREADY_DECIDED = 'this proposal was already decided';

// A queue read shows nothing, then a skeleton, then a note, then Retry, then
// gives up. The request itself is what ends the wait; the clock only says
// which of those the owner is looking at.
export function queueReadPhase(elapsedMs) {
  if (elapsedMs < 200) return 'quiet';
  if (elapsedMs < 4000) return 'skeleton';
  if (elapsedMs < 10000) return 'noted';
  if (elapsedMs < 30000) return 'retry';
  return 'gave-up';
}

// A dismiss shows its spinner once the wait is perceptible, then the two
// sentences that say the server may already have recorded it.
export function dismissWait(elapsedMs) {
  if (elapsedMs < 200) return { spinner: false, note: '' };
  if (elapsedMs < 4000) return { spinner: true, note: '' };
  if (elapsedMs < 10000) return { spinner: true, note: DISMISS_STILL_SAVING };
  return { spinner: true, note: DISMISS_STILL_WAITING };
}

// Open proposals are the ones still acceptable. The cap is three, counted
// across every thread and workspace, which is the list's pending minus the
// ones that can no longer be accepted.
export function openProposalCount(pending, pendingStale) {
  const n = Number(pending);
  if (!Number.isFinite(n) || n <= 0) return 0;
  const stale = Number(pendingStale);
  const closed = Number.isFinite(stale) && stale > 0 ? stale : 0;
  return Math.max(0, n - closed);
}

export function queuePaused(pending, pendingStale) {
  return openProposalCount(pending, pendingStale) >= 3;
}

export function proposalNoLongerApplies(proposal) {
  if (proposal?.open !== false) return '';
  return 'No longer applies: ' + (proposal.staleCause || '');
}

// A create whose name is already a learned skill is the loop's own file,
// left behind by an accept that did not finish. The server only leaves a
// create open in that case, so the row can say so without a new field.
export function proposalFileOnDisk(proposal, skills) {
  if (proposal?.op !== 'create' || !proposal.name) return false;
  return (skills || []).some((row) => row?.name === proposal.name && row?.source === 'nusashell');
}

export function dismissArchiveSentence(name) {
  return `Dismissing moves ${name}'s file to the archive only if it is still exactly what the loop wrote; otherwise it stays.`;
}

export function dismissClosedHelper(name) {
  return `Closing this holds nothing: the loop may propose ${name} again. The reason is kept but not read.`;
}

// The long helper is only for a proposal the server says is still open.
// A proposal that is not open gets the short one. Anything else gets neither.
export function dismissSheetCopy(proposal, skills) {
  if (!proposal) return { help: '', file: '', patch: '' };
  const file = proposalFileOnDisk(proposal, skills) ? dismissArchiveSentence(proposal.name) : '';
  const patch = proposal.op === 'patch' ? DISMISS_PATCH : '';
  const help = proposal.open === true ? DISMISS_READS : proposal.open === false ? dismissClosedHelper(proposal.name) : '';
  return { help, file, patch };
}

export function dismissedNotice(name, open) {
  if (open) return `Dismissed ${name}. The loop will not propose ${name} the same way again for 30 days.`;
  return `Dismissed ${name}.`;
}

// What a dismiss answer means. A dropped connection is not a failure: the
// server may have recorded it. A proposal already decided is finished, not
// an error. The sentences are the matrix's, except a refusal the matrix
// does not rename, which keeps the server's own words.
export function dismissFailure(err) {
  if (skillMoveUnknown(err)) return { kind: 'unknown' };
  if (!(err instanceof ApiError)) return { kind: 'rejected', message: errorText(err) || DISMISS_NOT_RECORDED };
  if (err.status === 409 && err.message === ALREADY_DECIDED) return { kind: 'decided' };
  if (err.status === 404) return { kind: 'gone', message: DISMISS_GONE };
  if (err.status === 400 && err.message.includes('superseded:')) return { kind: 'reword', message: DISMISS_SUPERSEDED };
  if (err.status === 401) return { kind: 'login' };
  if (err.status === 500) return { kind: 'retry', message: DISMISS_NOT_RECORDED };
  return { kind: 'rejected', message: errorText(err) || DISMISS_NOT_RECORDED };
}

// The dismiss reason survives the component, and a reload after the session
// lapses, so the sheet can open again with the same words. The owner sends
// it; nothing here sends it for them.
const dismissDrafts = new Map();
const DISMISS_DRAFT_KEY = 'nusashell.skillDismissDraft';
let dismissReopen = '';

function persistDismissDrafts() {
  try {
    if (typeof sessionStorage === 'undefined') return;
    sessionStorage.setItem(DISMISS_DRAFT_KEY, JSON.stringify({ reopen: dismissReopen, drafts: [...dismissDrafts] }));
  } catch { /* the copy in memory still covers this page */ }
}

export function loadDismissDrafts() {
  try {
    if (typeof sessionStorage === 'undefined') return;
    const raw = sessionStorage.getItem(DISMISS_DRAFT_KEY);
    if (!raw) return;
    const saved = JSON.parse(raw);
    if (!saved || !Array.isArray(saved.drafts)) return;
    for (const row of saved.drafts) {
      if (Array.isArray(row) && row[0] && typeof row[1] === 'string' && row[1]) dismissDrafts.set(row[0], row[1]);
    }
    if (typeof saved.reopen === 'string' && dismissDrafts.has(saved.reopen)) dismissReopen = saved.reopen;
  } catch { /* a bad copy does not block the page */ }
}
loadDismissDrafts();

export function dismissDraft(id) { return dismissDrafts.get(id) || ''; }

export function setDismissDraft(id, reason) {
  if (!id) return;
  const text = String(reason ?? '');
  if (text) {
    dismissDrafts.set(id, text);
    dismissReopen = id;
  } else {
    dismissDrafts.delete(id);
    if (dismissReopen === id) dismissReopen = '';
  }
  persistDismissDrafts();
}

export function dismissReopenId() { return dismissReopen; }

export function resetDismissDrafts() {
  dismissDrafts.clear();
  dismissReopen = '';
  try { if (typeof sessionStorage !== 'undefined') sessionStorage.removeItem(DISMISS_DRAFT_KEY); } catch { /* nothing stored */ }
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
