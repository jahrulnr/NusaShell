import type { DomainEvent } from "@nusashell/domain";

export interface JobCompletedEvent extends DomainEvent {
  readonly type: "job.completed";
  readonly jobId: string;
  readonly name: string;
  readonly summary: string;
}

export interface JobFailedEvent extends DomainEvent {
  readonly type: "job.failed";
  readonly jobId: string;
  readonly name: string;
  readonly error: string;
}

export function createJobCompletedEvent(
  jobId: string,
  name: string,
  summary: string,
  occurredAt = new Date(),
): JobCompletedEvent {
  return {
    type: "job.completed",
    aggregateId: jobId,
    occurredAt,
    jobId,
    name,
    summary,
  };
}

export function createJobFailedEvent(
  jobId: string,
  name: string,
  error: string,
  occurredAt = new Date(),
): JobFailedEvent {
  return {
    type: "job.failed",
    aggregateId: jobId,
    occurredAt,
    jobId,
    name,
    error,
  };
}
