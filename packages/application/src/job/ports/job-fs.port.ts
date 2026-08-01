/**
 * Filesystem operations the {@link JobScheduler} needs for output persistence
 * and tick-lock coordination. Implemented by an infrastructure adapter so the
 * application layer stays free of `node:fs` imports.
 */
export interface JobFsPort {
  /**
   * Persist a job run output file.
   * @returns the absolute file path, or `null` if persistence failed.
   */
  persistJobOutput(jobId: string, stamp: string, content: string): Promise<string | null>;

  /** Acquire an exclusive tick lock with stale-PID reaping. */
  acquireTickLock(): Promise<boolean>;

  /** Release the tick lock (best-effort). */
  releaseTickLock(): Promise<void>;
}
