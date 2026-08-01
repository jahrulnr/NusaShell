/**
 * Job automation waist — domain model (pure application layer).
 *
 * A Job is a durable, time-triggered unit of work that fires either a
 * headless agent turn or a direct plugin tool call. Jobs run only while
 * NusaShell is open (see docs/architecture/job-automation.md).
 */

import type { ReasoningEffort } from "../agent/ports/agent-provider.port.js";

export type JobSchedule =
  | { readonly kind: "once"; readonly runAt: string } // ISO timestamp
  | { readonly kind: "interval"; readonly minutes: number }
  | { readonly kind: "cron"; readonly expr: string }; // 5-field

export type JobMode =
  | {
      readonly type: "agent";
      readonly prompt: string;
      readonly providerId?: string;
      readonly model?: string;
      readonly effort?: ReasoningEffort;
    }
  | {
      readonly type: "tool";
      readonly pluginId: string;
      readonly toolName: string;
      readonly args: Readonly<Record<string, unknown>>;
    };

export type JobStatus = "ok" | "error" | "cancelled" | null;

export interface Job {
  readonly id: string;
  readonly name: string;
  readonly schedule: JobSchedule;
  readonly mode: JobMode;
  readonly enabled: boolean;
  /** null = repeat forever */
  readonly repeat: { readonly times: number | null; readonly completed: number };
  readonly nextRunAt: string | null;
  readonly lastRunAt: string | null;
  readonly lastStatus: JobStatus;
  readonly lastError: string | null;
  readonly createdAt: string;
}

export interface JobOutputEntry {
  readonly jobId: string;
  readonly runAt: string;
  readonly status: "ok" | "error" | "cancelled";
  readonly summary: string;
  readonly path: string;
  readonly traceId?: string;
}

/** One-shot grace window: a `once` job older than this at tick time is missed. */
export const ONCE_GRACE_SECONDS = 120;

/** Catchup grace for recurring jobs: max(120s, min(period/2, 2h)). */
export function recurringCatchupGraceSeconds(periodMinutes: number): number {
  const halfPeriod = Math.floor((periodMinutes * 60) / 2);
  const capped = Math.min(halfPeriod, 2 * 60 * 60);
  return Math.max(ONCE_GRACE_SECONDS, capped);
}

export function isRecurring(schedule: JobSchedule): boolean {
  return schedule.kind !== "once";
}
