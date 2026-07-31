import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { SqliteDatabase, SqliteJobStore } from "../src/persistence/sqlite/index.js";
import { JsonJobStore } from "../src/persistence/json/index.js";
import type { Job, JobStorePort } from "@nusashell/application";

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-1",
    name: "Test job",
    schedule: { kind: "interval", minutes: 30 },
    mode: { type: "agent", prompt: "Say hello" },
    enabled: true,
    repeat: { times: null, completed: 0 },
    nextRunAt: "2025-01-01T00:30:00.000Z",
    lastRunAt: null,
    lastStatus: null,
    lastError: null,
    createdAt: "2025-01-01T00:00:00.000Z",
    ...overrides,
  };
}

const NOW = new Date("2025-01-01T00:00:00Z");

function runStoreContract(name: string, makeStore: () => Promise<{ store: JobStorePort; cleanup: () => Promise<void> }>): void {
  describe(name, () => {
    let store: JobStorePort;
    let cleanup: () => Promise<void>;

    beforeEach(async () => {
      const env = await makeStore();
      store = env.store;
      cleanup = env.cleanup;
    });

    afterEach(async () => {
      await cleanup();
    });

    it("create + get roundtrip", async () => {
      const job = makeJob();
      await store.create(job);
      const found = await store.get("job-1");
      expect(found).not.toBeNull();
      expect(found!.name).toBe("Test job");
      expect(found!.schedule).toEqual({ kind: "interval", minutes: 30 });
      expect(found!.mode).toEqual({ type: "agent", prompt: "Say hello" });
    });

    it("list returns all jobs", async () => {
      await store.create(makeJob({ id: "a", createdAt: "2025-01-01T00:00:00.000Z" }));
      await store.create(makeJob({ id: "b", createdAt: "2025-01-02T00:00:00.000Z" }));
      const list = await store.list();
      expect(list).toHaveLength(2);
    });

    it("update mutates fields", async () => {
      await store.create(makeJob());
      const updated = await store.update(makeJob({ name: "Renamed", enabled: false }));
      expect(updated.name).toBe("Renamed");
      const found = await store.get("job-1");
      expect(found!.name).toBe("Renamed");
      expect(found!.enabled).toBe(false);
    });

    it("remove deletes a job", async () => {
      await store.create(makeJob());
      await store.remove("job-1");
      expect(await store.get("job-1")).toBeNull();
    });

    it("markRun advances counters and disables one-shot", async () => {
      const once = makeJob({
        id: "once-1",
        schedule: { kind: "once", runAt: "2025-01-01T00:00:10.000Z" },
        nextRunAt: "2025-01-01T00:00:10.000Z",
        repeat: { times: 1, completed: 0 },
      });
      await store.create(once);
      const result = await store.markRun("once-1", "ok", null, null, NOW);
      expect(result).not.toBeNull();
      expect(result!.enabled).toBe(false);
      expect(result!.repeat.completed).toBe(1);
      expect(result!.lastStatus).toBe("ok");
      expect(result!.lastRunAt).toBe(NOW.toISOString());
    });

    it("markRun disables recurring job when repeat limit reached", async () => {
      const job = makeJob({
        id: "bounded-1",
        schedule: { kind: "interval", minutes: 30 },
        repeat: { times: 2, completed: 1 },
      });
      await store.create(job);
      const result = await store.markRun("bounded-1", "ok", null, "2025-01-01T01:00:00.000Z", NOW);
      expect(result!.enabled).toBe(false);
      expect(result!.repeat.completed).toBe(2);
    });

    it("markRun keeps recurring job enabled under the limit", async () => {
      await store.create(makeJob({ repeat: { times: 5, completed: 0 } }));
      const result = await store.markRun("job-1", "ok", null, "2025-01-01T00:30:00.000Z", NOW);
      expect(result!.enabled).toBe(true);
      expect(result!.repeat.completed).toBe(1);
    });

    it("claimFire is at-most-once", async () => {
      await store.create(makeJob());
      const first = await store.claimFire("job-1", "claim-a", 60, NOW);
      const second = await store.claimFire("job-1", "claim-b", 60, NOW);
      expect(first).toBe(true);
      expect(second).toBe(false);
    });

    it("claimFire reaps expired claims", async () => {
      await store.create(makeJob());
      await store.claimFire("job-1", "claim-a", 60, NOW);
      const later = new Date(NOW.getTime() + 120 * 1000);
      const reclaimed = await store.claimFire("job-1", "claim-b", 60, later);
      expect(reclaimed).toBe(true);
    });

    it("releaseFire frees a claim", async () => {
      await store.create(makeJob());
      await store.claimFire("job-1", "claim-a", 60, NOW);
      await store.releaseFire("job-1", "claim-a");
      const reclaimed = await store.claimFire("job-1", "claim-b", 60, NOW);
      expect(reclaimed).toBe(true);
    });

    it("listDue returns enabled jobs with nextRunAt <= now and no live claim", async () => {
      await store.create(makeJob({ id: "due-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
      await store.create(makeJob({ id: "future-1", nextRunAt: "2025-06-01T00:00:00.000Z" }));
      await store.create(makeJob({ id: "disabled-1", enabled: false, nextRunAt: "2024-12-31T23:00:00.000Z" }));
      const due = await store.listDue(NOW);
      expect(due.map((j) => j.id)).toEqual(["due-1"]);
    });

    it("listDue skips jobs with a live claim", async () => {
      await store.create(makeJob({ id: "due-1", nextRunAt: "2024-12-31T23:00:00.000Z" }));
      await store.claimFire("due-1", "claim-a", 60, NOW);
      const due = await store.listDue(NOW);
      expect(due).toHaveLength(0);
    });

    it("appendOutput + listOutputs", async () => {
      await store.create(makeJob());
      await store.appendOutput("job-1", {
        jobId: "job-1",
        runAt: NOW.toISOString(),
        status: "ok",
        summary: "done",
        path: "/tmp/out.md",
      });
      const outputs = await store.listOutputs("job-1", 10);
      expect(outputs).toHaveLength(1);
      expect(outputs[0]!.summary).toBe("done");
    });
  });
}

describe("JobStore implementations", () => {
  let tempDir: string;
  let db: SqliteDatabase;

  runStoreContract("SqliteJobStore", async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-job-sqlite-"));
    db = new SqliteDatabase(join(tempDir, "jobs.db"));
    const store = new SqliteJobStore(db);
    return {
      store,
      cleanup: async () => {
        db.close();
        await rm(tempDir, { recursive: true, force: true });
      },
    };
  });

  runStoreContract("JsonJobStore", async () => {
    const dir = await mkdtemp(join(tmpdir(), "nusashell-job-json-"));
    const store = new JsonJobStore(dir);
    return {
      store,
      cleanup: async () => {
        await rm(dir, { recursive: true, force: true });
      },
    };
  });
});
