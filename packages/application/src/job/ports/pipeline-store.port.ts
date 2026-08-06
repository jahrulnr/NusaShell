import type {
  Pipeline,
  PipelineRun,
  PipelineStatus,
} from "../pipeline-model.js";

/**
 * Persistence port for pipeline definitions and durable runs.
 * Single-process desktop: claim is atomic within this store's write queue.
 */
export interface PipelineStorePort {
  create(pipeline: Pipeline): Promise<Pipeline>;
  update(pipeline: Pipeline): Promise<Pipeline>;
  get(id: string): Promise<Pipeline | null>;
  list(): Promise<readonly Pipeline[]>;
  remove(id: string): Promise<void>;

  /**
   * Atomically claim a new run for a pipeline (max concurrency 1).
   * Returns null when the definition is missing or another non-terminal run is active.
   */
  claimRun(run: PipelineRun): Promise<PipelineRun | null>;

  /** Persist mid-run state (heartbeat, step transitions). No-op if run is terminal. */
  updateRun(run: PipelineRun): Promise<PipelineRun | null>;

  /**
   * Terminal transition + denormalize pipeline lastStatus.
   * Idempotent for the same runId once already terminal.
   */
  finalizeRun(
    run: PipelineRun,
    status: Extract<PipelineStatus, "ok" | "error" | "cancelled" | "interrupted">,
    error: string | null,
    now: Date,
  ): Promise<PipelineRun | null>;

  getRun(runId: string): Promise<PipelineRun | null>;
  getActiveRun(pipelineId: string): Promise<PipelineRun | null>;
  listRuns(pipelineId: string, limit?: number): Promise<readonly PipelineRun[]>;

  /**
   * Enabled schedule-triggered pipelines due to fire (nextRunAt <= now)
   * with no non-terminal active run.
   */
  listDueSchedules(now: Date): Promise<readonly Pipeline[]>;

  /**
   * Mark non-terminal runs with expired leases as interrupted.
   * Returns number of recovered runs.
   */
  recoverExpiredLeases(now: Date): Promise<number>;

  /**
   * @deprecated Prefer finalizeRun. Kept for compatibility during transition.
   */
  markRun(
    id: string,
    status: "ok" | "error" | "cancelled",
    error: string | null,
    now: Date,
  ): Promise<Pipeline | null>;
}
