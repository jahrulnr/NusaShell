/**
 * Job automation waist — domain model (pure application layer).
 *
 * A Job is a durable unit of work that fires either a headless agent turn or
 * a direct plugin tool call. A job's **trigger** determines *when* it fires:
 * on a schedule (once/interval/cron) or in response to an event emitted by a
 * plugin. Jobs run only while NusaShell is open
 * (see docs/architecture/job-automation.md and
 * tmp/plan/watch-to-agent/02-job-triggers.md).
 */

import type { ReasoningEffort } from "../agent/ports/agent-provider.port.js";

export type JobSchedule =
  | { readonly kind: "once"; readonly runAt: string } // ISO timestamp
  | { readonly kind: "interval"; readonly minutes: number }
  | { readonly kind: "cron"; readonly expr: string }; // 5-field

/**
 * A single AND-condition evaluated against an event payload before an
 * event-triggered job is dispatched. v1 supports `eq` | `contains` | `regex`
 * only (see 02-job-triggers.md §4). Phase D adds `ne` and `ConditionGroup`
 * for OR/NOT/nested matching.
 */
export interface Condition {
  readonly path: string; // dot-path into the event, e.g. "payload.subject"
  readonly op: "eq" | "contains" | "regex" | "ne";
  readonly value: string; // for regex: a JS regex source string
}

/**
 * A group of conditions combined with a logical operator. Phase D adds OR
 * and NOT support (see 06-roadmap.md Phase D). A `ConditionGroup` can
 * contain leaf `Condition`s or nested `ConditionGroup`s.
 */
export type ConditionNode =
  | Condition
  | { readonly op: "or"; readonly any: readonly ConditionNode[] }
  | { readonly op: "not"; readonly of: ConditionNode };

/**
 * Trigger union: a job fires either on a schedule or on a matching event.
 *
 * - `schedule` wraps the existing `JobSchedule` shape (once/interval/cron).
 * - `event` matches an `AutomationEvent` by glob pattern, optional pluginId
 *   pre-filter, AND-conditions, and per-job throttle/cap guards.
 */
export type JobTrigger =
  | { readonly kind: "schedule"; readonly schedule: JobSchedule }
  | {
      readonly kind: "event";
      readonly pattern: string;
      readonly pluginId?: string;
      readonly conditions?: readonly ConditionNode[];
      readonly throttleMs?: number;
      readonly maxFiresPerHour?: number;
    };

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

/**
 * Optional emit-on-complete hook (Phase D soft chains). When set, the
 * scheduler emits an `AutomationEvent` with the given type + payload after
 * the job finishes successfully. Another event-job can match this event,
 * forming a chain without a full DAG. Cycle guards prevent infinite loops.
 */
export interface OnCompleteEmit {
  readonly type: string;
  readonly payload?: Readonly<Record<string, unknown>>;
}

export interface Job {
  readonly id: string;
  readonly name: string;
  readonly trigger: JobTrigger;
  readonly mode: JobMode;
  readonly enabled: boolean;
  /** null = repeat forever */
  readonly repeat: { readonly times: number | null; readonly completed: number };
  readonly nextRunAt: string | null;
  readonly lastRunAt: string | null;
  readonly lastStatus: JobStatus;
  readonly lastError: string | null;
  readonly createdAt: string;
  /** Phase D: emit an automation event when this job completes successfully. */
  readonly onComplete?: OnCompleteEmit;
}

/**
 * Backward-compatible read-path migration: if a persisted job blob has the
 * legacy top-level `schedule` field and no `trigger`, synthesize the new
 * `trigger: { kind: "schedule", schedule: <old> }` shape. This keeps old
 * stores working without a destructive migration (see 02-job-triggers.md §11).
 */
export function normalizeTrigger(raw: {
  trigger?: JobTrigger;
  schedule?: JobSchedule;
}): JobTrigger {
  if (raw.trigger) return raw.trigger;
  if (raw.schedule) return { kind: "schedule", schedule: raw.schedule };
  throw new Error("Job is missing both `trigger` and legacy `schedule`");
}

/** Extract the schedule from a trigger, or null for event triggers. */
export function scheduleOf(trigger: JobTrigger): JobSchedule | null {
  return trigger.kind === "schedule" ? trigger.schedule : null;
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
