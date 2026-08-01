import type { EventEnvelope } from "@nusashell/contracts";
import type { JobCompletedEvent, JobFailedEvent, ApplicationEvent } from "@nusashell/application";

/**
 * Maps job-domain events (completed/failed) to WS event envelopes.
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
        payload: { jobId: e.jobId, name: e.name, summary: e.summary, timestamp },
      };
    }
    case "job.failed": {
      const e = event as JobFailedEvent;
      return {
        kind: "event",
        event: "job.failed",
        sequence,
        payload: { jobId: e.jobId, name: e.name, error: e.error, timestamp },
      };
    }
    default:
      return null;
  }
}
