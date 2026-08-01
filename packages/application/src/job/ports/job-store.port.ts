import type { Job, JobOutputEntry } from "../job-model.js";

export interface JobStorePort {
  create(job: Job): Promise<Job>;
  update(job: Job): Promise<Job>;
  get(id: string): Promise<Job | null>;
  list(): Promise<readonly Job[]>;
  remove(id: string): Promise<void>;
  /** Record a run result and advance repeat/nextRunAt counters. */
  markRun(
    id: string,
    status: "ok" | "error" | "cancelled",
    error: string | null,
    nextRunAt: string | null,
    now: Date,
  ): Promise<Job | null>;
  /** Fire-time dedup: claim a dispatch slot with a TTL. Returns true if claimed. */
  claimFire(jobId: string, claimId: string, ttlSeconds: number, now: Date): Promise<boolean>;
  /** Release a claim before TTL (e.g. after successful dispatch). */
  releaseFire(jobId: string, claimId: string): Promise<void>;
  /** Enabled jobs whose nextRunAt <= now. */
  listDue(now: Date): Promise<readonly Job[]>;
  /** Persist a run output entry (metadata only; content is on disk). */
  appendOutput(jobId: string, entry: JobOutputEntry): Promise<void>;
  /** Recent output entries for a job (newest first). */
  listOutputs(jobId: string, limit: number): Promise<readonly JobOutputEntry[]>;
}
