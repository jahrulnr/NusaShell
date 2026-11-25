// A room's cross-thread queue depth.
//
// The server announces it as `conversation.queued { conversationId, waiting }`
// whenever a message from another thread lands in a room's queue and whenever
// the queue drains. The frame carries no seq and is never replayed, so the
// number in it is the room's current one: the last frame wins, in arrival
// order, with no attempt to order events.
//
// It lives outside the store on purpose. A count that moves while the owner is
// reading must not re-render the transcript; the one line that shows it
// subscribes here and re-renders itself.
const counts = new Map();
const listeners = new Set();

// setQueued records a room's waiting count and tells the line that shows it.
// It answers whether anything changed, so a repeated frame costs nothing.
export function setQueued(roomId, waiting) {
  if (!roomId) return false;
  const next = Math.max(0, Math.trunc(Number(waiting) || 0));
  const prev = counts.get(roomId) || 0;
  if (prev === next) return false;
  if (next) counts.set(roomId, next);
  else counts.delete(roomId);
  for (const fn of [...listeners]) fn(roomId, next);
  return true;
}

// queuedFor answers how many messages wait in a room right now.
export function queuedFor(roomId) {
  return counts.get(roomId) || 0;
}

// watchQueued hears every change; it returns the unsubscribe.
export function watchQueued(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
