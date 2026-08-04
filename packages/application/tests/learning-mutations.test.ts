import { describe, expect, it, vi } from "vitest";
import { LearningGraphService, type LearningGraphDeps } from "../src/index.js";
import type {
  SkillRegistryPort,
  SkillUsagePort,
  SkillProvenancePort,
  SkillSummary,
  SkillUsageRecord,
  SkillReadResult,
} from "../src/index.js";
import type { MemoryStorePort, MemorySnapshot, MemoryMutationResult } from "../src/index.js";

function fakeSummary(id: string): SkillSummary {
  return { id, name: id, description: "test", fileCount: 1, updatedAt: "2025-06-01T00:00:00Z" };
}

function fakeUsageRecord(skillId: string, overrides: Partial<SkillUsageRecord> = {}): SkillUsageRecord {
  return {
    skillId,
    useCount: 1,
    viewCount: 0,
    patchCount: 0,
    lastUsedAt: "2025-06-01T00:00:00Z",
    lastViewedAt: null,
    lastPatchedAt: null,
    state: "active",
    pinned: false,
    archivedAt: null,
    createdAt: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

function fakeSnapshot(memory: string[] = [], user: string[] = []): MemorySnapshot {
  const memEntries = memory.map((text) => ({ text, createdAt: null as string | null }));
  const userEntries = user.map((text) => ({ text, createdAt: null as string | null }));
  return {
    memory: memEntries,
    user: userEntries,
    usage: {
      memory: { chars: memEntries.map((e) => e.text).join("").length, limit: 2200 },
      user: { chars: userEntries.map((e) => e.text).join("").length, limit: 1375 },
    },
  };
}

function makeMutationDeps(overrides: Partial<LearningGraphDeps> = {}): LearningGraphDeps {
  return {
    registry: {
      list: async () => [fakeSummary("test-skill")],
      read: async () => ({ skillId: "test-skill", path: "SKILL.md", content: "# Test", sizeBytes: 0, editable: true, truncated: false }) as SkillReadResult,
      get: async () => ({ id: "test-skill", name: "test-skill", description: "test", fileCount: 1, updatedAt: "2025-06-01T00:00:00Z", files: [] }),
      write: vi.fn(async () => ({ skillId: "test-skill", path: "SKILL.md", content: "", sizeBytes: 0, editable: true, truncated: false })),
      archive: vi.fn(async () => {}),
    } as unknown as SkillRegistryPort,
    usage: {
      listRecords: async () => [fakeUsageRecord("test-skill")],
      getRecord: async () => fakeUsageRecord("test-skill"),
    } as unknown as SkillUsagePort,
    provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
    memoryStore: {
      loadSnapshot: async () => fakeSnapshot(["alpha note", "beta note"], ["user note"]),
      replace: vi.fn(async (): Promise<MemoryMutationResult> => ({ ok: true, data: { entries: [], usage: { chars: 0, limit: 2200 } } })),
      remove: vi.fn(async (): Promise<MemoryMutationResult> => ({ ok: true, data: { entries: [], usage: { chars: 0, limit: 2200 } } })),
    } as unknown as MemoryStorePort,
    ...overrides,
  };
}

describe("LearningGraphService.deleteNode", () => {
  it("archives a builtin skill and marks it deleted so it is not re-seeded", async () => {
    const archiveSpy = vi.fn(async () => {});
    const markBuiltinDeletedSpy = vi.fn(async () => {});
    const deps = makeMutationDeps({
      registry: {
        list: async () => [fakeSummary("skill-creator")],
        archive: archiveSpy,
      } as unknown as SkillRegistryPort,
      provenance: {
        get: async () => "builtin",
        markBuiltinDeleted: markBuiltinDeletedSpy,
      } as unknown as SkillProvenancePort,
    });
    const result = await new LearningGraphService(deps).deleteNode("skill-creator");

    expect(result).toEqual({ ok: true });
    expect(archiveSpy).toHaveBeenCalledWith("skill-creator");
    expect(markBuiltinDeletedSpy).toHaveBeenCalledWith("skill-creator");
  });

  it("archives skill on delete (not permanent delete)", async () => {
    const archiveSpy = vi.fn(async () => {});
    const deps = makeMutationDeps({
      registry: {
        list: async () => [fakeSummary("my-skill")],
        read: async () => ({ skillId: "my-skill", path: "SKILL.md", content: "# My", sizeBytes: 0, editable: true, truncated: false }),
        get: async () => ({ id: "my-skill", name: "my-skill", description: "test", fileCount: 1, updatedAt: "2025-06-01T00:00:00Z", files: [] }),
        archive: archiveSpy,
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("my-skill")],
        getRecord: async () => fakeUsageRecord("my-skill", { pinned: false }),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.deleteNode("my-skill");

    expect(result.ok).toBe(true);
    expect(archiveSpy).toHaveBeenCalledWith("my-skill");
  });

  it("refuses to delete a pinned skill and points at unpin", async () => {
    const archiveSpy = vi.fn(async () => {});
    const deps = makeMutationDeps({
      registry: {
        list: async () => [fakeSummary("pinned-skill")],
        read: async () => ({ skillId: "pinned-skill", path: "SKILL.md", content: "", sizeBytes: 0, editable: true, truncated: false }),
        get: async () => ({ id: "pinned-skill", name: "pinned-skill", description: "", fileCount: 1, updatedAt: "", files: [] }),
        archive: archiveSpy,
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("pinned-skill", { pinned: true })],
        getRecord: async () => fakeUsageRecord("pinned-skill", { pinned: true }),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.deleteNode("pinned-skill");

    expect(result.ok).toBe(false);
    expect(result.code).toBe("pinned");
    expect(result.error).toMatch(/unpin/i);
    expect(archiveSpy).not.toHaveBeenCalled();
  });

  it("removes the correct memory entry on delete", async () => {
    const removeSpy = vi.fn(async (): Promise<MemoryMutationResult> => ({ ok: true, data: { entries: [], usage: { chars: 0, limit: 2200 } } }));
    const deps = makeMutationDeps({
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["alpha note", "beta note"], ["user note"]),
        remove: removeSpy,
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.deleteNode("memory:memory:1");

    expect(result.ok).toBe(true);
    expect(removeSpy).toHaveBeenCalledWith("memory", "beta note");
  });

  it("returns node_stale when memory entry no longer exists", async () => {
    const deps = makeMutationDeps({
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["only one"]),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.deleteNode("memory:memory:5");

    expect(result.ok).toBe(false);
    expect(result.code).toBe("node_stale");
  });
});

describe("LearningGraphService.editNode", () => {
  it("writes SKILL.md content for skill nodes", async () => {
    const writeSpy = vi.fn(async () => ({ skillId: "edit-skill", path: "SKILL.md", content: "", sizeBytes: 0, editable: true, truncated: false }));
    const deps = makeMutationDeps({
      registry: {
        list: async () => [fakeSummary("edit-skill")],
        read: async () => ({ skillId: "edit-skill", path: "SKILL.md", content: "# Old", sizeBytes: 0, editable: true, truncated: false }),
        get: async () => ({ id: "edit-skill", name: "edit-skill", description: "", fileCount: 1, updatedAt: "", files: [] }),
        write: writeSpy,
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("edit-skill")],
        getRecord: async () => fakeUsageRecord("edit-skill"),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.editNode("edit-skill", "# New content");

    expect(result.ok).toBe(true);
    expect(writeSpy).toHaveBeenCalledWith("edit-skill", "SKILL.md", "# New content");
  });

  it("replaces the correct memory entry on edit", async () => {
    const replaceSpy = vi.fn(async (): Promise<MemoryMutationResult> => ({ ok: true, data: { entries: [], usage: { chars: 0, limit: 2200 } } }));
    const deps = makeMutationDeps({
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["alpha note", "beta note"], []),
        replace: replaceSpy,
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.editNode("memory:memory:0", "updated note");

    expect(result.ok).toBe(true);
    expect(replaceSpy).toHaveBeenCalledWith("memory", "alpha note", "updated note");
  });

  it("rejects empty content for memory edit", async () => {
    const deps = makeMutationDeps();
    const service = new LearningGraphService(deps);
    const result = await service.editNode("memory:memory:0", "   ");

    expect(result.ok).toBe(false);
    expect(result.code).toBe("empty_content");
  });

  it("returns node_stale when memory entry no longer matches", async () => {
    const deps = makeMutationDeps({
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["only one"]),
        replace: vi.fn(async () => { throw new Error("old_text did not match any entry"); }),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const result = await service.editNode("memory:memory:0", "new content");

    expect(result.ok).toBe(false);
    expect(result.code).toBe("node_stale");
  });
});

describe("LearningGraphService.getNode", () => {
  it("returns skill node detail with SKILL.md content", async () => {
    const deps = makeMutationDeps({
      registry: {
        list: async () => [fakeSummary("detail-skill")],
        read: async () => ({ skillId: "detail-skill", path: "SKILL.md", content: "# Detail\nContent here", sizeBytes: 20, editable: true, truncated: false }),
        get: async () => ({ id: "detail-skill", name: "detail-skill", description: "test", fileCount: 1, updatedAt: "2025-06-01T00:00:00Z", files: [] }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("detail-skill")],
        getRecord: async () => fakeUsageRecord("detail-skill"),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const detail = await service.getNode("detail-skill");

    expect(detail.kind).toBe("skill");
    expect(detail.content).toBe("# Detail\nContent here");
    expect(detail.editable).toBe(true);
  });

  it("returns memory node detail with entry text", async () => {
    const deps = makeMutationDeps({
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["memory text here"], ["user text"]),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const detail = await service.getNode("memory:user:1");

    expect(detail.kind).toBe("memory");
    expect(detail.content).toBe("user text");
    expect(detail.memorySource).toBe("user");
  });
});
