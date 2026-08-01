import type { EventEnvelope } from "@nusashell/contracts";
import type { JobCompletedEvent, JobFailedEvent, JobStartedEvent, JobCancelledEvent, ApplicationEvent } from "@nusashell/application";

/**
 * Maps job-domain events (started/completed/failed/cancelled) to WS event envelopes.
 */
export function mapJobEvent(
  event: ApplicationEvent,
  sequence: number,
  timestamp: string,
): EventEnvelope | null {
  switch (event.type) {
    case "job.completed": {
      const e = event as JobCompletedEvent;
      return {
        kind: "event",
        event: "job.completed",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          summary: e.summary,
          timestamp,
          ...(e.traceId !== undefined ? { traceId: e.traceId } : {}),
        },
      };
    }
    case "job.failed": {
      const e = event as JobFailedEvent;
      return {
        kind: "event",
        event: "job.failed",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          error: e.error,
          timestamp,
          ...(e.traceId !== undefined ? { traceId: e.traceId } : {}),
        },
      };
    }
    case "job.started": {
      const e = event as JobStartedEvent;
      return {
        kind: "event",
        event: "job.started",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          traceId: e.traceId,
          startedAt: e.startedAt,
          timestamp,
        },
      };
    }
    case "job.cancelled": {
      const e = event as JobCancelledEvent;
      return {
        kind: "event",
        event: "job.cancelled",
        sequence,
        payload: {
          jobId: e.jobId,
          name: e.name,
          traceId: e.traceId,
          timestamp,
        },
      };
    }
    default:
      return null;
  }
}
