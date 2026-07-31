/**
 * Assigns a monotonic `streamSeq` per `traceId` for agent/ACP streaming events.
 *
 * The counter is scoped per traceId so parallel tool/text events stay ordered
 * within a turn even when the global `EventEnvelope.sequence` interleaves with
 * plugin/job events. Assign at the application publish site (container
 * callbacks / ACP session service) so the WS transport stays a dumb
 * broadcaster. Clear the counter when a turn ends.
 */
export class StreamSeqRegistry {
  private readonly counters = new Map<string, number>();

  /** Returns the next streamSeq for `traceId` (starts at 1). */
  next(traceId: string): number {
    const value = (this.counters.get(traceId) ?? 0) + 1;
    this.counters.set(traceId, value);
    return value;
  }

  /** Current streamSeq for `traceId` (0 if none emitted yet). */
  peek(traceId: string): number {
    return this.counters.get(traceId) ?? 0;
  }

  /** Drops the counter for `traceId`. Safe to call when no counter exists. */
  clear(traceId: string): void {
    this.counters.delete(traceId);
  }
}
