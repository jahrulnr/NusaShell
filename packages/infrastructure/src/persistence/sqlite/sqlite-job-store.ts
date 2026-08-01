import type { JobStorePort } from "@nusashell/application";
import type {
  Job,
  JobSchedule,
  JobMode,
  JobOutputEntry,
} from "@nusashell/application";
import type { SqliteDatabase } from "./database.js";

interface JobRow {
  id: string;
  name: string;
  schedule_json: string;
  mode_json: string;
  enabled: number;
  repeat_times: number | null;
  repeat_completed: number;
  next_run_at: string | null;
  last_run_at: string | null;
  last_status: string | null;
  last_error: string | null;
  created_at: string;
}

interface ClaimRow {
  job_id: string;
  claim_id: string;
  expires_at: string;
}

interface OutputRow {
  id: number;
  job_id: string;
  run_at: string;
  status: string;
  summary: string;
  path: string;
  trace_id?: string | null;
}

/**
 * SQLite-backed durable JobStore. Reuses the shared WAL `SqliteDatabase` and
 * the `002-jobs.sql` migration. Schedule/mode are stored as JSON sidecars so
 * the pure application layer owns their shape.
 */
export class SqliteJobStore implements JobStorePort {
  constructor(private readonly database: SqliteDatabase) {}

  async create(job: Job): Promise<Job> {
    this.database.raw
      .prepare(
        `INSERT INTO jobs
         (id, name, schedule_json, mode_json, enabled, repeat_times, repeat_completed,
          next_run_at, last_run_at, last_status, last_error, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        job.id,
        job.name,
        JSON.stringify(job.schedule),
        JSON.stringify(job.mode),
        job.enabled ? 1 : 0,
        job.repeat.times,
        job.repeat.completed,
        job.nextRunAt,
        job.lastRunAt,
        job.lastStatus,
        job.lastError,
        job.createdAt,
      );
    return job;
  }

  async update(job: Job): Promise<Job> {
    this.database.raw
      .prepare(
        `UPDATE jobs SET
           name = ?, schedule_json = ?, mode_json = ?, enabled = ?,
           repeat_times = ?, repeat_completed = ?, next_run_at = ?,
           last_run_at = ?, last_status = ?, last_error = ?
         WHERE id = ?`,
      )
      .run(
        job.name,
        JSON.stringify(job.schedule),
        JSON.stringify(job.mode),
        job.enabled ? 1 : 0,
        job.repeat.times,
        job.repeat.completed,
        job.nextRunAt,
        job.lastRunAt,
        job.lastStatus,
        job.lastError,
        job.id,
      );
    return job;
  }

  async get(id: string): Promise<Job | null> {
    const row = this.database.raw
      .prepare("SELECT * FROM jobs WHERE id = ?")
      .get(id) as JobRow | undefined;
    return row ? rowToJob(row) : null;
  }

  async list(): Promise<readonly Job[]> {
    const rows = this.database.raw
      .prepare("SELECT * FROM jobs ORDER BY created_at DESC")
      .all() as JobRow[];
    return rows.map(rowToJob);
  }

  async remove(id: string): Promise<void> {
    this.database.raw
      .prepare("DELETE FROM job_outputs WHERE job_id = ?")
      .run(id);
    this.database.raw
      .prepare("DELETE FROM job_claims WHERE job_id = ?")
      .run(id);
    this.database.raw.prepare("DELETE FROM jobs WHERE id = ?").run(id);
  }

  async markRun(
    id: string,
    status: "ok" | "error",
    error: string | null,
    nextRunAt: string | null,
    now: Date,
  ): Promise<Job | null> {
    const existing = await this.get(id);
    if (!existing) return null;
    const completed = existing.repeat.completed + 1;
    const times = existing.repeat.times;
    // Disable when a one-shot completed, or when a bounded repeat limit is reached.
    const limitReached = times !== null && completed >= times;
    const enabled = existing.schedule.kind === "once"
      ? false
      : limitReached ? false : existing.enabled;
    this.database.raw
      .prepare(
        `UPDATE jobs SET
           enabled = ?, repeat_completed = ?, next_run_at = ?,
           last_run_at = ?, last_status = ?, last_error = ?
         WHERE id = ?`,
      )
      .run(
        enabled ? 1 : 0,
        completed,
        nextRunAt,
        now.toISOString(),
        status,
        error,
        id,
      );
    return (await this.get(id)) ?? null;
  }

  async claimFire(jobId: string, claimId: string, ttlSeconds: number, now: Date): Promise<boolean> {
    const db = this.database.raw;
    // Reap expired claims for this job first.
    db.prepare("DELETE FROM job_claims WHERE job_id = ? AND expires_at <= ?")
      .run(jobId, now.toISOString());
    const existing = db
      .prepare("SELECT claim_id FROM job_claims WHERE job_id = ?")
      .get(jobId) as { claim_id: string } | undefined;
    if (existing) return false;
    const expiresAt = new Date(now.getTime() + ttlSeconds * 1000).toISOString();
    db.prepare("INSERT INTO job_claims (job_id, claim_id, expires_at) VALUES (?, ?, ?)")
      .run(jobId, claimId, expiresAt);
    return true;
  }

  async releaseFire(jobId: string, claimId: string): Promise<void> {
    this.database.raw
      .prepare("DELETE FROM job_claims WHERE job_id = ? AND claim_id = ?")
      .run(jobId, claimId);
  }

  async listDue(now: Date): Promise<readonly Job[]> {
    const rows = this.database.raw
      .prepare(
        `SELECT * FROM jobs
         WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
         ORDER BY next_run_at ASC`,
      )
      .all(now.toISOString()) as JobRow[];
    const jobs = rows.map(rowToJob);
    // Filter out jobs with a live claim.
    const nowIso = now.toISOString();
    const result: Job[] = [];
    for (const job of jobs) {
      this.database.raw
        .prepare("DELETE FROM job_claims WHERE job_id = ? AND expires_at <= ?")
        .run(job.id, nowIso);
      const claimed = this.database.raw
        .prepare("SELECT claim_id FROM job_claims WHERE job_id = ?")
        .get(job.id) as { claim_id: string } | undefined;
      if (!claimed) result.push(job);
    }
    return result;
  }

  async appendOutput(jobId: string, entry: JobOutputEntry): Promise<void> {
    this.database.raw
      .prepare(
        `INSERT INTO job_outputs (job_id, run_at, status, summary, path, trace_id)
         VALUES (?, ?, ?, ?, ?, ?)`,
      )
      .run(entry.jobId, entry.runAt, entry.status, entry.summary, entry.path, entry.traceId ?? null);
  }

  async listOutputs(jobId: string, limit: number): Promise<readonly JobOutputEntry[]> {
    const rows = this.database.raw
      .prepare(
        `SELECT * FROM job_outputs WHERE job_id = ? ORDER BY id DESC LIMIT ?`,
      )
      .all(jobId, Math.max(1, Math.min(limit, 100))) as OutputRow[];
    return rows.map((row) => ({
      jobId: row.job_id,
      runAt: row.run_at,
      status: row.status as "ok" | "error" | "cancelled",
      summary: row.summary,
      path: row.path,
      ...(row.trace_id != null ? { traceId: row.trace_id } : {}),
    }));
  }
}

function rowToJob(row: JobRow): Job {
  const schedule = JSON.parse(row.schedule_json) as JobSchedule;
  const mode = JSON.parse(row.mode_json) as JobMode;
  return {
    id: row.id,
    name: row.name,
    schedule,
    mode,
    enabled: row.enabled === 1,
    repeat: { times: row.repeat_times, completed: row.repeat_completed },
    nextRunAt: row.next_run_at,
    lastRunAt: row.last_run_at,
    lastStatus: (row.last_status as Job["lastStatus"]) ?? null,
    lastError: row.last_error,
    createdAt: row.created_at,
  };
}
