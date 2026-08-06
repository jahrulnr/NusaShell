import type { DomainEvent } from "@nusashell/domain";
import type {
  PipelineRunStatus,
  PipelineStepRunStatus,
  PipelineTriggerSource,
} from "../job/pipeline-model.js";

export interface PipelineStartedEvent extends DomainEvent {
  readonly type: "pipeline.started";
  readonly pipelineId: string;
  readonly name: string;
  readonly runId: string;
  readonly traceId: string;
  readonly triggerSource: PipelineTriggerSource;
  readonly startedAt: string;
}

export interface PipelineCompletedEvent extends DomainEvent {
  readonly type: "pipeline.completed";
  readonly pipelineId: string;
  readonly name: string;
  readonly runId: string;
  readonly traceId: string;
  readonly summary: string;
}

export interface PipelineFailedEvent extends DomainEvent {
  readonly type: "pipeline.failed";
  readonly pipelineId: string;
  readonly name: string;
  readonly runId: string;
  readonly traceId: string;
  readonly error: string;
  readonly errorCode?: string;
}

export interface PipelineCancelledEvent extends DomainEvent {
  readonly type: "pipeline.cancelled";
  readonly pipelineId: string;
  readonly name: string;
  readonly runId: string;
  readonly traceId: string;
}

export interface PipelineStepUpdatedEvent extends DomainEvent {
  readonly type: "pipeline.step_updated";
  readonly pipelineId: string;
  readonly runId: string;
  readonly traceId: string;
  readonly stepId: string;
  readonly status: PipelineStepRunStatus;
  readonly summary?: string;
  readonly error?: string;
  readonly runStatus: PipelineRunStatus;
}

export function createPipelineStartedEvent(
  pipelineId: string,
  name: string,
  runId: string,
  traceId: string,
  triggerSource: PipelineTriggerSource,
  startedAt: Date,
): PipelineStartedEvent {
  return {
    type: "pipeline.started",
    aggregateId: pipelineId,
    occurredAt: startedAt,
    pipelineId,
    name,
    runId,
    traceId,
    triggerSource,
    startedAt: startedAt.toISOString(),
  };
}

export function createPipelineCompletedEvent(
  pipelineId: string,
  name: string,
  runId: string,
  traceId: string,
  summary: string,
  occurredAt = new Date(),
): PipelineCompletedEvent {
  return {
    type: "pipeline.completed",
    aggregateId: pipelineId,
    occurredAt,
    pipelineId,
    name,
    runId,
    traceId,
    summary,
  };
}

export function createPipelineFailedEvent(
  pipelineId: string,
  name: string,
  runId: string,
  traceId: string,
  error: string,
  occurredAt = new Date(),
  errorCode?: string,
): PipelineFailedEvent {
  return {
    type: "pipeline.failed",
    aggregateId: pipelineId,
    occurredAt,
    pipelineId,
    name,
    runId,
    traceId,
    error,
    ...(errorCode !== undefined ? { errorCode } : {}),
  };
}

export function createPipelineCancelledEvent(
  pipelineId: string,
  name: string,
  runId: string,
  traceId: string,
  occurredAt = new Date(),
): PipelineCancelledEvent {
  return {
    type: "pipeline.cancelled",
    aggregateId: pipelineId,
    occurredAt,
    pipelineId,
    name,
    runId,
    traceId,
  };
}

export function createPipelineStepUpdatedEvent(
  pipelineId: string,
  runId: string,
  traceId: string,
  stepId: string,
  status: PipelineStepRunStatus,
  runStatus: PipelineRunStatus,
  occurredAt = new Date(),
  summary?: string,
  error?: string,
): PipelineStepUpdatedEvent {
  return {
    type: "pipeline.step_updated",
    aggregateId: pipelineId,
    occurredAt,
    pipelineId,
    runId,
    traceId,
    stepId,
    status,
    runStatus,
    ...(summary !== undefined ? { summary } : {}),
    ...(error !== undefined ? { error } : {}),
  };
}
