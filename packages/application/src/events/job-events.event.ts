import type { DomainEvent } from "@nusashell/domain";
import type { JobMode } from "../job/job-model.js";

export interface JobCompletedEvent extends DomainEvent {
  readonly type: "job.completed";
  readonly jobId: string;
  readonly name: string;
  readonly summary: string;
  readonly traceId?: string;
}

export interface JobFailedEvent extends DomainEvent {
  readonly type: "job.failed";
  readonly jobId: string;
  readonly name: string;
  readonly error: string;
  readonly traceId?: string;
}

export interface JobStartedEvent extends DomainEvent {
  readonly type: "job.started";
  readonly jobId: string;
  readonly name: string;
  readonly traceId: string;
  readonly startedAt: string;
  readonly mode: JobMode;
}

export interface JobCancelledEvent extends DomainEvent {
  readonly type: "job.cancelled";
  readonly jobId: string;
  readonly name: string;
  readonly traceId: string;
}

export function createJobCompletedEvent(
  jobId: string,
  name: string,
  summary: string,
  occurredAt = new Date(),
  traceId?: string,
): JobCompletedEvent {
  return {
    type: "job.completed",
    aggregateId: jobId,
    occurredAt,
    jobId,
    name,
    summary,
    ...(traceId !== undefined ? { traceId } : {}),
  };
}

export function createJobFailedEvent(
  jobId: string,
  name: string,
  error: string,
  occurredAt = new Date(),
  traceId?: string,
): JobFailedEvent {
  return {
    type: "job.failed",
    aggregateId: jobId,
    occurredAt,
    jobId,
    name,
    error,
    ...(traceId !== undefined ? { traceId } : {}),
  };
}

export function createJobStartedEvent(
  jobId: string,
  name: string,
  traceId: string,
  startedAt: Date,
  mode: JobMode,
): JobStartedEvent {
  return {
    type: "job.started",
    aggregateId: jobId,
    occurredAt: startedAt,
    jobId,
    name,
    traceId,
    startedAt: startedAt.toISOString(),
    mode,
  };
}

export function createJobCancelledEvent(
  jobId: string,
  name: string,
  traceId: string,
  occurredAt = new Date(),
): JobCancelledEvent {
  return {
    type: "job.cancelled",
    aggregateId: jobId,
    occurredAt,
    jobId,
    name,
    traceId,
  };
}
