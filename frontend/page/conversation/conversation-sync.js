// Merge an authoritative conversation snapshot into the mounted event list.
//
// The prototype receives deltas over SSE, while a second NusaShell client can
// append a user message before this tab sees the next lifecycle event. A full
// replace would make the reader lose its scroll position and can flash while
// widgets reconnect. Keep mounted identities, adopt persisted fields in
// place, and only append genuinely new snapshot events or patch widgets whose
// rendered data actually changed.
//
// Dedup is identity-only: every row carries the persisted message id (the
// optimistic send bubble is keyed by turns.start's user_message_id), so a
// snapshot copy always folds into the mounted row instead of appending a
// twin. Earlier fuzzy content/attachment matching broke whenever the backend
// rewrote the payload after send (file_path on disk save, trimmed text) —
// that heuristic was removed rather than widened per attachment type.

function semanticKey(event) {
  const messageId = event?.messageId || event?.sourceMessageId;
  if (!messageId || !event?.type || event.type === 'user.message') return '';
  if (event.type === 'tool.run') return `${messageId}:tool:${event.toolCallId || event.tool || ''}`;
  return `${messageId}:${event.type}`;
}

function sameValue(left, right) {
  if (Object.is(left, right)) return true;
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false;
    return left.every((value, index) => sameValue(value, right[index]));
  }
  if (left && right && typeof left === 'object' && typeof right === 'object') {
    const keys = new Set([...Object.keys(left), ...Object.keys(right)]);
    return [...keys].every((key) => sameValue(left[key], right[key]));
  }
  return false;
}

function sameRenderedEvent(current, incoming) {
  // These fields describe the mounted identity or optimistic reconciliation,
  // not the widget's visible content. Adoption of an optimistic row is
  // handled separately so it still gets one intentional update.
  const ignored = new Set(['id', 'sourceId', 'optimistic']);
  const keys = new Set([...Object.keys(current || {}), ...Object.keys(incoming || {})]);
  return [...keys].filter((key) => !ignored.has(key)).every((key) => sameValue(current?.[key], incoming?.[key]));
}

function terminalStatus(value) {
  const status = String(value || '').toLowerCase();
  return ['complete', 'completed', 'done', 'ok', 'failed', 'error', 'interrupted', 'cancelled', 'canceled'].includes(status);
}

// A lifecycle snapshot can have been requested before the live SSE stream
// received more text/output. Never let that older snapshot visibly rewind a
// live widget; a later authoritative snapshot can still replace it normally.
function mergeSnapshotEvent(current, incoming) {
  const merged = { ...current, ...incoming };
  if (!String(current?.id || '').startsWith('live-') || current.id === incoming.id) return merged;

  for (const key of ['markdown', 'content', 'output', 'summary', 'command']) {
    if (typeof current?.[key] === 'string' && typeof incoming?.[key] === 'string'
      && current[key].length > incoming[key].length) merged[key] = current[key];
  }
  for (const key of ['details', 'transcript']) {
    if (Array.isArray(current?.[key]) && Array.isArray(incoming?.[key])
      && JSON.stringify(current[key]).length > JSON.stringify(incoming[key]).length) merged[key] = current[key];
  }
  if (terminalStatus(current?.status) && !terminalStatus(incoming?.status)) merged.status = current.status;
  return merged;
}

// Lifecycle WS payloads identify the new assistant message. Fetching the
// conversation remains the RPC contract, but only this turn (and its nearby
// user prompt) belongs in the live append transaction; history stays mounted.
export function selectConversationTurnMessages(messages = [], messageID = '') {
  if (!Array.isArray(messages) || !messageID) return Array.isArray(messages) ? messages : [];
  const index = messages.findIndex((message) => message?.id === messageID);
  if (index < 0) return [];
  const start = messages[index - 1]?.role === 'user' ? index - 1 : index;
  return messages.slice(start, index + 1);
}

export function mergeConversationEvents(target, snapshot) {
  if (!Array.isArray(target) || !Array.isArray(snapshot)) return [];
  const byId = new Map();
  const bySourceId = new Map();
  const bySemantic = new Map();
  for (const event of target) {
    if (!event?.id) continue;
    byId.set(event.id, event);
    if (event.sourceId) bySourceId.set(event.sourceId, event);
    const key = semanticKey(event);
    if (key) bySemantic.set(key, event);
  }

  const changed = [];
  for (const incoming of snapshot) {
    if (!incoming?.id) continue;
    const liveId = `live-${incoming.id}`;
    let current = byId.get(incoming.id) || bySourceId.get(incoming.id) || byId.get(liveId)
      || bySemantic.get(semanticKey(incoming));

    if (!current) {
      current = { ...incoming };
      target.push(current);
      byId.set(current.id, current);
      const key = semanticKey(current);
      if (key) bySemantic.set(key, current);
      changed.push(current);
      continue;
    }

    const mountedId = current.id;
    const wasOptimistic = Boolean(current.optimistic);
    // An optimistic row landing on an already-mounted authoritative row (the
    // turn.started snapshot can beat the turns.start response) must not
    // overwrite persisted fields — e.g. the attachment file_path the server
    // filled on disk save — with its pre-send payload.
    const merged = incoming.optimistic && !current.optimistic
      ? { ...incoming, ...current }
      : mergeSnapshotEvent(current, incoming);
    const renderedChanged = !sameRenderedEvent(current, merged);
    Object.assign(current, merged);
    // The DOM is keyed by the live/optimistic id. Changing it during a
    // snapshot merge would make the next delta mount a duplicate widget.
    current.id = mountedId;
    if (wasOptimistic || incoming.optimistic) current.optimistic = false;
    if (mountedId !== incoming.id) current.sourceId = incoming.id;
    byId.set(mountedId, current);
    bySourceId.set(incoming.id, current);
    const key = semanticKey(current);
    if (key) bySemantic.set(key, current);
    if (renderedChanged || wasOptimistic || incoming.optimistic) changed.push(current);
  }
  return changed;
}
