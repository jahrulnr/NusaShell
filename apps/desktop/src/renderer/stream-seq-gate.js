/**
 * Per-traceId stream sequence gate for agent/ACP streaming events.
 *
 * The backend assigns a monotonic `streamSeq` (starting at 1) per traceId on
 * every agent/ACP streaming event. The gate drops stale/duplicate events
 * (streamSeq <= last seen for that traceId) and flags gaps (streamSeq jumps
 * by more than 1) so the presenter can mark a turn incomplete.
 *
 * Events without a `streamSeq` (e.g. legacy/non-streaming) are accepted
 * unchanged so the gate is safe to wrap around existing handlers.
 *
 * @returns {{ check: (traceId: string, streamSeq?: number) => { accept: boolean, gap: boolean }, clear: (traceId: string) => void, lastSeen: (traceId: string) => number }}
 */
export function createStreamSeqGate() {
  const seen = new Map();

  /**
   * @param {string} traceId
   * @param {number} [streamSeq]
   * @returns {{ accept: boolean, gap: boolean }}
   */
  function check(traceId, streamSeq) {
    if (streamSeq === undefined || streamSeq === null || Number.isNaN(streamSeq)) {
      return { accept: true, gap: false };
    }
    const last = seen.get(traceId) ?? 0;
    if (streamSeq <= last) {
      return { accept: false, gap: false };
    }
    const gap = streamSeq > last + 1;
    seen.set(traceId, streamSeq);
    return { accept: true, gap };
  }

  function clear(traceId) {
    seen.delete(traceId);
  }

  function lastSeen(traceId) {
    return seen.get(traceId) ?? 0;
  }

  return { check, clear, lastSeen };
}
