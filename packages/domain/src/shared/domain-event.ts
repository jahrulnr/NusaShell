export interface DomainEvent {
  readonly type: string;
  readonly occurredAt: Date;
  readonly aggregateId: string;
  /**
   * Monotonic per-`aggregateId` (traceId) stream sequence for agent/ACP
   * streaming events. Assigned at the application publish site so the WS
   * transport stays a dumb broadcaster. Undefined for non-streaming events.
   */
  readonly streamSeq?: number;
}
