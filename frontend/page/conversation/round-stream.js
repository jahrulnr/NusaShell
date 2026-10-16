export function acceptRoundFrame(cursor, frame) {
  const seq = Number(frame?.seq);
  if (!Number.isSafeInteger(seq) || seq <= 0) return { accept: true, gap: false };
  const last = Number(cursor?.lastSeq) || 0;
  if (seq <= last) return { accept: false, gap: false };
  if (seq !== last + 1) return { accept: false, gap: true };
  cursor.lastSeq = seq;
  return { accept: true, gap: false };
}

export function roundDoneNeedsReplay(cursor, done) {
  const reported = Number(done?.last_seq);
  return Number.isSafeInteger(reported) && reported > (Number(cursor?.lastSeq) || 0);
}

export function roundStreamURL(round, cursor = {}) {
  const params = new URLSearchParams({ run_id: round.run_id, message_id: round.message_id });
  if (cursor.lastSeq > 0) params.set('after', String(cursor.lastSeq));
  return `/stream?${params}`;
}
