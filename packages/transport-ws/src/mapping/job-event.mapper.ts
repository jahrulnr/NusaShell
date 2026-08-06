import type { EventEnvelope } from "@nusashell/contracts";
import type {
  ApplicationEvent,
  JobCompletedEvent,
  JobFailedEvent,
  JobStartedEvent,
  JobCancelledEvent,
  PipelineStartedEvent,
  PipelineCompletedEvent,
  PipelineFailedEvent,
  PipelineCancelledEvent,
  PipelineStepUpdatedEvent,
} from "@nusashell/application";

/**
 * Maps job and pipeline domain events to WS event envelopes.
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
    case "pipeline.started": {
      const e = event as PipelineStartedEvent;
      return {
        kind: "event",
        event: "pipeline.started",
        sequence,
        payload: {
          pipelineId: e.pipelineId,
          name: e.name,
          runId: e.runId,
          traceId: e.traceId,
          triggerSource: e.triggerSource,
          startedAt: e.startedAt,
          timestamp,
        },
      };
    }
    case "pipeline.completed": {
      const e = event as PipelineCompletedEvent;
      return {
        kind: "event",
        event: "pipeline.completed",
        sequence,
        payload: {
          pipelineId: e.pipelineId,
          name: e.name,
          runId: e.runId,
          traceId: e.traceId,
          summary: e.summary,
          timestamp,
        },
      };
    }
    case "pipeline.failed": {
      const e = event as PipelineFailedEvent;
      return {
        kind: "event",
        event: "pipeline.failed",
        sequence,
        payload: {
          pipelineId: e.pipelineId,
          name: e.name,
          runId: e.runId,
          traceId: e.traceId,
          error: e.error,
          timestamp,
          ...(e.errorCode !== undefined ? { errorCode: e.errorCode } : {}),
        },
      };
    }
    case "pipeline.cancelled": {
      const e = event as PipelineCancelledEvent;
      return {
        kind: "event",
        event: "pipeline.cancelled",
        sequence,
        payload: {
          pipelineId: e.pipelineId,
          name: e.name,
          runId: e.runId,
          traceId: e.traceId,
          timestamp,
        },
      };
    }
    case "pipeline.step_updated": {
      const e = event as PipelineStepUpdatedEvent;
      return {
        kind: "event",
        event: "pipeline.step_updated",
        sequence,
        payload: {
          pipelineId: e.pipelineId,
          runId: e.runId,
          traceId: e.traceId,
          stepId: e.stepId,
          status: e.status,
          runStatus: e.runStatus,
          timestamp,
          ...(e.summary !== undefined ? { summary: e.summary } : {}),
          ...(e.error !== undefined ? { error: e.error } : {}),
        },
      };
    }
    default:
      return null;
  }
}
