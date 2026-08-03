/**
 * Pipeline DAG model — multi-step orchestration (Phase E).
 *
 * A Pipeline is a directed acyclic graph of steps. Each step runs an agent
 * turn or tool call, optionally conditioned on accumulated context from
 * prior steps. Steps declare `dependsOn` (step IDs that must complete first),
 * and the scheduler runs them in topological order.
 *
 * Unlike the v1.1 soft chain (onComplete.emitEvent), a Pipeline has:
 * - An explicit step graph (not just shared event-type strings).
 * - Accumulated context (`context[outputKey]` per step).
 * - Per-step conditions evaluated against that context.
 * - A single pipeline-level trigger (schedule or event).
 *
 * See tmp/plan/mcp-automation/03-job-pipeline.md for the full spec.
 */

import type { ReasoningEffort } from "../agent/ports/agent-provider.port.js";
import type { ConditionNode } from "./job-model.js";
import type { JobTrigger } from "./job-model.js";

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
  /** Per-step timeout in ms (0 = use pipeline default). */
  readonly timeoutMs?: number;
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

/**
 * A pipeline's runtime status.
 */
export type PipelineStatus = "ok" | "error" | "cancelled" | "running" | null;

/**
 * A multi-step orchestration DAG.
 */
export interface Pipeline {
  readonly id: string;
  readonly name: string;
  readonly description?: string;
  readonly enabled: boolean;
  /** What triggers this pipeline (schedule or event). */
  readonly trigger: JobTrigger;
  readonly steps: readonly PipelineStep[];
  readonly settings?: PipelineSettings;
  readonly createdAt: string;
  readonly lastRunAt: string | null;
  readonly lastStatus: PipelineStatus;
  readonly lastError: string | null;
}

export interface PipelineSettings {
  readonly maxConcurrency?: number;
  readonly timeoutMs?: number;
  readonly maxRetries?: number;
}

/**
 * Per-run context accumulated as steps complete. Each step that has an
 * `outputKey` stores its result here, making it available to downstream
 * steps' conditions and prompt templates.
 */
export type PipelineContext = Readonly<Record<string, unknown>>;

/**
 * Result of a single step execution.
 */
export interface PipelineStepResult {
  readonly stepId: string;
  readonly status: "ok" | "error" | "skipped";
  readonly summary: string;
  readonly output?: unknown;
  readonly error?: string;
  readonly startedAt: string;
  readonly completedAt: string;
}

/**
 * Result of a full pipeline run.
 */
export interface PipelineRunResult {
  readonly pipelineId: string;
  readonly status: PipelineStatus;
  readonly context: PipelineContext;
  readonly stepResults: readonly PipelineStepResult[];
  readonly startedAt: string;
  readonly completedAt: string;
  readonly error?: string;
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
