// A room's queue depth: how many jobs wait behind the turn that runs there.
//
// The number is the worker's queue for the room, so a message from another
// thread, an ordinary send, and an edit all count the same. The server
// announces it as `conversation.queued { conversationId, waiting }` whenever it
// changes and whenever the queue drains. The frame carries no seq and is never
// replayed, so the number in it is the room's current one: the last frame wins,
// in arrival order, with no attempt to order events.
//
// It lives outside the store on purpose. A count that moves while the owner is
// reading must not re-render the transcript; the one line that shows it
// subscribes here and re-renders itself.
const counts = new Map();
// unknown holds the rooms whose wait is still there but whose depth is not: a
// reconnect can lose the frame that carried the number, and a line must not go
// on claiming a count the tab can no longer vouch for.
const unknown = new Set();
const listeners = new Set();

// setQueued records a room's waiting count and tells the line that shows it.
// It answers whether anything changed, so a repeated frame costs nothing.
export function setQueued(roomId, waiting) {
  if (!roomId) return false;
  const next = Math.max(0, Math.trunc(Number(waiting) || 0));
  const prev = counts.get(roomId) || 0;
  // A frame is the newest word even when it repeats the last number: it also
  // ends a wait whose depth was unknown.
  const unknownBefore = unknown.delete(roomId);
  if (!unknownBefore && prev === next) return false;
  if (next) counts.set(roomId, next);
  else counts.delete(roomId);
  for (const fn of [...listeners]) fn(roomId, next);
  return true;
}

// queuedFor answers how many jobs wait in a room right now: a count, 0 for a
// room that is not waiting, or null for a room known to be waiting whose depth
// this tab lost track of.
export function queuedFor(roomId) {
  if (unknown.has(roomId)) return null;
  return counts.get(roomId) || 0;
}

// resetQueued forgets a room's depth but not the wait: the queue may well still
// be there, so the line keeps saying something waits, without a number it can
// no longer vouch for. A later frame brings the number back (or ends the wait).
export function resetQueued(roomId) {
  if (!counts.has(roomId) && unknown.has(roomId)) return false;
  const had = counts.delete(roomId);
  unknown.add(roomId);
  for (const fn of [...listeners]) fn(roomId, null);
  return had;
}

// forgetQueued drops a room's line entirely. It is for a wait that cannot
// survive: the queue lives in the server's memory, and a server that says it is
// shutting down takes every queue with it.
export function forgetQueued(roomId) {
  const had = counts.delete(roomId) || unknown.delete(roomId);
  if (!had) return false;
  for (const fn of [...listeners]) fn(roomId, 0);
  return true;
}

// queuedNote is the room's line for that count. It names what waits — jobs to
// run — and not who sent them: an ordinary send or an edit is in the same
// number. The composer's tray says whose messages those are ("of yours"); the
// two lines sit on one screen at times, so they must not read as one counter.
export const queuedNote = (n) => (n == null ? 'waiting to run' : `${n} waiting to run`);

// ownNote is the tray's line for the messages this tab queued behind the turn.
// The tray's number is the owner's own queue, not the worker's depth, so it
// says whose it is and never uses queuedNote's words.
export function ownNote(n, { held = false, command = false } = {}) {
  const when = held ? 'on hold' : command ? 'it sends when the command finishes' : 'it sends when this turn ends';
  return `${n} of yours queued — ${when}`;
}

// watchQueued hears every change; it returns the unsubscribe.
export function watchQueued(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
