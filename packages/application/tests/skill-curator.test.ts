import { describe, expect, it, vi } from "vitest";
import {
  SkillCuratorService,
  DEFAULT_CURATOR_SETTINGS,
  type SkillCuratorDeps,
} from "../src/index.js";
import type {
  SkillRegistryPort,
  SkillProvenancePort,
  SkillUsagePort,
  SkillSummary,
  SkillUsageRecord,
} from "../src/index.js";
import type { EventDispatcher } from "../src/events/event-dispatcher.js";

function fakeSummary(id: string): SkillSummary {
  return { id, name: id, description: "test", fileCount: 1, updatedAt: new Date().toISOString() };
}

function fakeUsageRecord(skillId: string, overrides: Partial<SkillUsageRecord> = {}): SkillUsageRecord {
  return {
    skillId,
    useCount: 0,
    viewCount: 0,
    patchCount: 0,
    lastUsedAt: null,
    lastViewedAt: null,
    lastPatchedAt: null,
    state: "active",
    pinned: false,
    archivedAt: null,
    createdAt: new Date("2025-01-01T00:00:00Z").toISOString(),
    ...overrides,
  };
}

function makeEventDispatcher(): EventDispatcher {
  return {
    publish: vi.fn(async () => {}),
    on: vi.fn(),
    onAny: vi.fn(),
    publishAll: vi.fn(),
  } as unknown as EventDispatcher;
}

function makeDeps(overrides: Partial<SkillCuratorDeps> = {}): SkillCuratorDeps {
  return {
    registry: {
      list: async () => [],
      archive: async () => {},
    } as unknown as SkillRegistryPort,
    provenance: {
      get: async () => "agent",
    } as unknown as SkillProvenancePort,
    usage: {
      getRecord: async () => fakeUsageRecord("test"),
      setState: async () => {},
      setPinned: async () => {},
    } as unknown as SkillUsagePort,
    ...overrides,
  };
}

describe("SkillCuratorService", () => {
  it("uses default settings", () => {
    const curator = new SkillCuratorService(makeDeps());
    expect(curator.getSettings()).toEqual(DEFAULT_CURATOR_SETTINGS);
  });

  it("transitions active → stale after staleAfterDays", async () => {
    const setState = vi.fn(async () => {});
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill"),
        setState,
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-01-31T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(1);
    expect(result.changes[0]).toMatchObject({ skillId: "agent-skill", from: "active", to: "stale" });
    expect(setState).toHaveBeenCalledWith("agent-skill", "stale");
  });

  it("transitions stale → archived after archiveAfterDays", async () => {
    const setState = vi.fn(async () => {});
    const archive = vi.fn(async () => {});
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")], archive } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill", { state: "stale" }),
        setState,
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-04-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(1);
    expect(result.changes[0]).toMatchObject({ skillId: "agent-skill", from: "stale", to: "archived" });
    expect(archive).toHaveBeenCalledWith("agent-skill");
    expect(setState).toHaveBeenCalledWith("agent-skill", "archived");
  });

  it("skips pinned skills", async () => {
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("pinned-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("pinned-skill", { pinned: true }),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(0);
  });

  it("skips user-owned skills when pruneUserOwned is false", async () => {
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("user-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "user" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("user-skill"),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90, pruneUserOwned: false });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(0);
  });

  it("dry-run reports changes without applying them", async () => {
    const setState = vi.fn(async () => {});
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill"),
        setState,
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    const result = await curator.run(true);
    expect(result.dryRun).toBe(true);
    expect(result.changes).toHaveLength(1);
    expect(setState).not.toHaveBeenCalled();
  });

  it("publishes event when changes occur (non-dry-run)", async () => {
    const eventDispatcher = makeEventDispatcher();
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill"),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      eventDispatcher,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    await curator.run(false);
    expect(eventDispatcher.publish).toHaveBeenCalledTimes(1);
    const event = (eventDispatcher.publish as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(event.type).toBe("agent.learning_updated");
    expect(event.kinds).toContain("skill_curator");
  });

  it("does not publish event on dry-run", async () => {
    const eventDispatcher = makeEventDispatcher();
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill"),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      eventDispatcher,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    await curator.run(true);
    expect(eventDispatcher.publish).not.toHaveBeenCalled();
  });

  it("first-run deferral — newly created skill is not immediately stale", async () => {
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("fresh-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("fresh-skill", { createdAt: new Date().toISOString() }),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date(),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ staleAfterDays: 30, archiveAfterDays: 90 });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(0);
  });

  it("disabled curator returns no changes", async () => {
    const deps = makeDeps({
      registry: { list: async () => [fakeSummary("agent-skill")] } as unknown as SkillRegistryPort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      usage: {
        getRecord: async () => fakeUsageRecord("agent-skill"),
        setState: async () => {},
        setPinned: async () => {},
      } as unknown as SkillUsagePort,
      now: () => new Date("2025-06-01T00:00:00Z"),
    });
    const curator = new SkillCuratorService(deps);
    curator.configure({ enabled: false });

    const result = await curator.run(false);
    expect(result.changes).toHaveLength(0);
  });
});
