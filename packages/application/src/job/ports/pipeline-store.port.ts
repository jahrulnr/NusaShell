import type { Pipeline } from "../pipeline-model.js";

/**
 * Persistence port for Pipeline entities (Phase E).
 * Mirrors JobStorePort's shape but for Pipeline aggregates.
 */
export interface PipelineStorePort {
  create(pipeline: Pipeline): Promise<Pipeline>;
  update(pipeline: Pipeline): Promise<Pipeline>;
  get(id: string): Promise<Pipeline | null>;
  list(): Promise<readonly Pipeline[]>;
  remove(id: string): Promise<void>;
  /** Record a run result and update lastRunAt/lastStatus. */
  markRun(
    id: string,
    status: "ok" | "error" | "cancelled",
    error: string | null,
    now: Date,
  ): Promise<Pipeline | null>;
}
