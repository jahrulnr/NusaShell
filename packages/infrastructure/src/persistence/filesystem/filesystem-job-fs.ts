import { mkdir, open, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { JobFsPort } from "@nusashell/application";

const TICK_LOCK_FILE = ".tick.lock";
const STALE_PID_AFTER_MS = 5 * 60 * 1000;

/**
 * Filesystem implementation of {@link JobFsPort}. Handles job output file
 * persistence and the exclusive tick-lock with stale-PID reaping.
 */
export class FilesystemJobFs implements JobFsPort {
  constructor(private readonly jobsRoot: string) {}

  async persistJobOutput(jobId: string, stamp: string, content: string): Promise<string | null> {
    try {
      const dir = resolve(this.jobsRoot, "output", jobId);
      await mkdir(dir, { recursive: true });
      const path = resolve(dir, `${stamp}.md`);
      await writeFile(path, content, "utf8");
      return path;
    } catch {
      return null;
    }
  }

  async readJobOutput(path: string): Promise<string | null> {
    try {
      return await readFile(path, "utf8");
    } catch {
      return null;
    }
  }

  async acquireTickLock(): Promise<boolean> {
    const lockPath = resolve(this.jobsRoot, TICK_LOCK_FILE);
    try {
      await mkdir(this.jobsRoot, { recursive: true });
      // Reap stale lock: if the lock file is older than STALE_PID_AFTER_MS, remove it.
      try {
        const entries = await readdir(this.jobsRoot);
        if (entries.includes(TICK_LOCK_FILE)) {
          const content = await readFile(lockPath, "utf8").catch(() => "");
          const pidMatch = /pid:(\d+)/.exec(content);
          const startedMatch = /started:(\S+)/.exec(content);
          const staleByAge = startedMatch
            ? Date.now() - new Date(startedMatch[1]!).getTime() > STALE_PID_AFTER_MS
            : false;
          const pidDead = pidMatch ? !processAlive(parseInt(pidMatch[1]!, 10)) : false;
          if (staleByAge || pidDead) {
            await rm(lockPath, { force: true });
          }
        }
      } catch {
        // best-effort
      }
      const fh = await open(lockPath, "wx");
      await fh.writeFile(`pid:${process.pid}\nstarted:${new Date().toISOString()}\n`);
      await fh.close();
      return true;
    } catch {
      return false;
    }
  }

  async releaseTickLock(): Promise<void> {
    const lockPath = resolve(this.jobsRoot, TICK_LOCK_FILE);
    await rm(lockPath, { force: true }).catch(() => {});
  }
}

function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}
