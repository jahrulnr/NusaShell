/**
 * Pipeline DAG model — multi-step orchestration.
 *
 * A Pipeline is a directed acyclic graph of steps. Each step runs an agent
 * turn or tool call, optionally conditioned on accumulated context from
 * prior steps. Steps declare `dependsOn` (step IDs that must complete first),
 * and the scheduler runs them in topological order (sequential; dependsOn is
 * ordering only in v1).
 *
 * Triggers: event, schedule (while NusaShell is open), and manual run.
 */

import type { ReasoningEffort } from "../agent/ports/agent-provider.port.js";
import type { ConditionNode, JobSchedule } from "./job-model.js";
import type { JobTrigger } from "./job-model.js";
import { ONCE_GRACE_SECONDS } from "./job-model.js";
import { computeNextRun } from "./schedule-parser.js";

export type { JobTrigger };

/**
 * A single step in a pipeline. Each step is one action (agent or tool)
 * that runs after its `dependsOn` steps complete successfully.
 */
export interface PipelineStep {
  /** Unique within the pipeline (e.g. "classify", "handle-urgent"). */
  readonly id: string;
  readonly name: string;
  readonly action: PipelineStepAction;
  /** Step IDs that must complete before this one runs. */
  readonly dependsOn?: readonly string[];
  /** Condition evaluated against accumulated context; skip if false. */
  readonly condition?: ConditionNode;
  /** Store this step's result as `context[outputKey]`. */
  readonly outputKey?: string;
}

export type PipelineStepAction =
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

/** Denormalized last-run status on the pipeline definition. */
export type PipelineStatus = "ok" | "error" | "cancelled" | "running" | "interrupted" | null;

/**
 * A multi-step orchestration DAG.
 */
export interface Pipeline {
  readonly id: string;
  readonly name: string;
  readonly description?: string;
  readonly enabled: boolean;
  /** What triggers this pipeline (event, schedule, or neither for manual-only). */
  readonly trigger: JobTrigger;
  readonly steps: readonly PipelineStep[];
  /** Optional runtime settings. Only timeoutMs is honored today. */
  readonly settings?: PipelineSettings;
  readonly createdAt: string;
  /** Next wall-clock fire for schedule triggers; null for event/manual/spent one-shot. */
  readonly nextRunAt: string | null;
  readonly lastRunAt: string | null;
  readonly lastStatus: PipelineStatus;
  readonly lastError: string | null;
  /** Last durable run id (if any). */
  readonly lastRunId?: string | null;
}

export interface PipelineSettings {
  /** Soft wall timeout for the whole run in ms (0 or omit = none). */
  readonly timeoutMs?: number;
}

/** Durable per-run status machine. Terminal states are irreversible by runId. */
export type PipelineRunStatus =
  | "claimed"
  | "running"
  | "ok"
  | "error"
  | "cancelled"
  | "interrupted";

export type PipelineStepRunStatus =
  | "queued"
  | "running"
  | "ok"
  | "error"
  | "skipped"
  | "cancelled";

export type PipelineTriggerSource = "manual" | "event" | "schedule";

export interface PipelineStepRun {
  readonly stepId: string;
  readonly status: PipelineStepRunStatus;
  /** Bounded human-readable summary (persist-safe). */
  readonly summary?: string;
  readonly error?: string;
  /** Bounded serialization of step output when present. */
  readonly outputPreview?: string;
  readonly outputTruncated?: boolean;
  readonly startedAt?: string;
  readonly completedAt?: string;
}

export interface PipelineRun {
  readonly runId: string;
  readonly pipelineId: string;
  readonly traceId: string;
  readonly status: PipelineRunStatus;
  readonly triggerSource: PipelineTriggerSource;
  readonly startedAt: string;
  readonly completedAt: string | null;
  readonly lastHeartbeatAt: string;
  readonly leaseExpiresAt: string;
  readonly currentStepId: string | null;
  readonly errorCode: string | null;
  readonly errorMessage: string | null;
  readonly stepRuns: readonly PipelineStepRun[];
}

export const TERMINAL_PIPELINE_RUN_STATUSES: ReadonlySet<PipelineRunStatus> = new Set([
  "ok",
  "error",
  "cancelled",
  "interrupted",
]);

export function isTerminalPipelineRunStatus(status: PipelineRunStatus): boolean {
  return TERMINAL_PIPELINE_RUN_STATUSES.has(status);
}

/**
 * Per-run context accumulated as steps complete. Each step that has an
 * `outputKey` stores its result here, making it available to downstream
 * steps' conditions and prompt templates.
 */
export type PipelineContext = Readonly<Record<string, unknown>>;

/**
 * Result of a single step execution (in-memory scheduler shape).
 */
export interface PipelineStepResult {
  readonly stepId: string;
  readonly status: "ok" | "error" | "skipped" | "cancelled";
  readonly summary: string;
  readonly output?: unknown;
  readonly error?: string;
  readonly startedAt: string;
  readonly completedAt: string;
}

/**
 * Result of a full pipeline run (returned to callers; also persisted).
 */
export interface PipelineRunResult {
  readonly runId: string;
  readonly pipelineId: string;
  readonly traceId: string;
  readonly status: Exclude<PipelineStatus, null>;
  readonly context: PipelineContext;
  readonly stepResults: readonly PipelineStepResult[];
  readonly startedAt: string;
  readonly completedAt: string;
  readonly error?: string;
  readonly errorCode?: string;
}

/**
 * Detect cycles in a pipeline's step graph. Returns the first cycle found
 * (as a list of step IDs), or null if the graph is acyclic.
 */
export function detectCycle(steps: readonly PipelineStep[]): string[] | null {
  const graph = new Map<string, readonly string[]>();
  for (const step of steps) {
    graph.set(step.id, step.dependsOn ?? []);
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const path: string[] = [];

  function dfs(nodeId: string): string[] | null {
    if (visiting.has(nodeId)) {
      const cycleStart = path.indexOf(nodeId);
      return path.slice(cycleStart).concat(nodeId);
    }
    if (visited.has(nodeId)) return null;
    visiting.add(nodeId);
    path.push(nodeId);
    const deps = graph.get(nodeId) ?? [];
    for (const dep of deps) {
      const cycle = dfs(dep);
      if (cycle) return cycle;
    }
    path.pop();
    visiting.delete(nodeId);
    visited.add(nodeId);
    return null;
  }

  for (const step of steps) {
    const cycle = dfs(step.id);
    if (cycle) return cycle;
  }
  return null;
}

/**
 * Topological sort of pipeline steps. Returns steps in dependency order.
 * Throws if a cycle is detected.
 */
export function topologicalSort(steps: readonly PipelineStep[]): PipelineStep[] {
  const cycle = detectCycle(steps);
  if (cycle) {
    throw new Error(`Pipeline has a cycle: ${cycle.join(" → ")}`);
  }
  const stepMap = new Map(steps.map((s) => [s.id, s]));
  const visited = new Set<string>();
  const result: PipelineStep[] = [];

  function visit(id: string): void {
    if (visited.has(id)) return;
    visited.add(id);
    const step = stepMap.get(id);
    if (!step) return;
    for (const dep of step.dependsOn ?? []) {
      visit(dep);
    }
    result.push(step);
  }

  for (const step of steps) {
    visit(step.id);
  }
  return result;
}

/**
 * Validate a pipeline's step graph. Returns an error message or null.
 */
export function validatePipeline(steps: readonly PipelineStep[]): string | null {
  if (steps.length === 0) return "Pipeline must have at least one step";
  const ids = new Set<string>();
  for (const step of steps) {
    if (ids.has(step.id)) return `Duplicate step id: ${step.id}`;
    ids.add(step.id);
  }
  for (const step of steps) {
    for (const dep of step.dependsOn ?? []) {
      if (!ids.has(dep)) return `Step "${step.id}" depends on unknown step "${dep}"`;
    }
  }
  const cycle = detectCycle(steps);
  if (cycle) return `Cycle detected: ${cycle.join(" → ")}`;
  return null;
}

/** Validate pipeline trigger shape (event or schedule). Manual run is a separate command. */
export function validatePipelineTrigger(trigger: JobTrigger): string | null {
  if (trigger.kind === "schedule") {
    const schedule = trigger.schedule;
    if (!schedule || typeof schedule !== "object") {
      return "Schedule trigger requires a schedule object";
    }
    if (schedule.kind === "once" && !schedule.runAt) {
      return "One-shot schedule requires runAt";
    }
    if (schedule.kind === "once" && schedule.runAt) {
      const runAtMs = new Date(schedule.runAt).getTime();
      if (Number.isNaN(runAtMs)) {
        return "One-shot runAt must be a valid ISO timestamp";
      }
      const ageSeconds = (Date.now() - runAtMs) / 1000;
      if (ageSeconds > ONCE_GRACE_SECONDS) {
        return `One-shot runAt is in the past beyond the ${ONCE_GRACE_SECONDS}s grace window`;
      }
    }
    if (schedule.kind === "interval" && (!(schedule.minutes > 0))) {
      return "Interval schedule requires positive minutes";
    }
    if (schedule.kind === "cron" && !schedule.expr?.trim()) {
      return "Cron schedule requires expr";
    }
    return null;
  }
  if (trigger.kind !== "event") {
    return "Pipeline trigger must be kind \"event\" or \"schedule\"";
  }
  if (!trigger.pattern?.trim()) {
    return "Event trigger requires a pattern";
  }
  if (isPipelineSelfEventPattern(trigger.pattern)) {
    return `Event pattern "${trigger.pattern}" is in the pipeline.* namespace; pipeline lifecycle events (pipeline.started/completed/failed/cancelled) are UI/telemetry events, not AutomationEvents, so a pipeline cannot trigger on them (self-trigger guard)`;
  }
  return null;
}

/**
 * True when an event pattern matches a type in the `pipeline.*` namespace.
 * Pipeline lifecycle events are published as domain events for UI/telemetry,
 * never as AutomationEvents, so an event trigger on `pipeline.*` can never
 * fire and would only enable a silent self-trigger loop. Reject them.
 */
export function isPipelineSelfEventPattern(pattern: string): boolean {
  if (!pattern || !pattern.trim()) return false;
  const trimmed = pattern.trim();
  // Bare prefix or any segment under `pipeline.` (also `pipeline.**`).
  return trimmed === "pipeline" || trimmed === "pipeline.*" || trimmed === "pipeline.**"
    || trimmed.startsWith("pipeline.");
}

/**
 * Initial or post-run next fire for a schedule trigger.
 * One-shots use runAt until fired; after a run (lastRunAt set) next is null.
 */
export function nextRunAtForPipelineTrigger(
  trigger: JobTrigger,
  lastRunAt: string | null,
  now: Date,
  enabled: boolean,
): string | null {
  if (!enabled || trigger.kind !== "schedule") return null;
  const schedule = trigger.schedule;
  if (schedule.kind === "once") {
    return lastRunAt ? null : schedule.runAt;
  }
  return computeNextRun(schedule, lastRunAt, now);
}

export function scheduleOfPipeline(trigger: JobTrigger): JobSchedule | null {
  return trigger.kind === "schedule" ? trigger.schedule : null;
}
