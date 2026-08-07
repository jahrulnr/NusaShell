import { describe, expect, it } from "vitest";
import {
  LearningGraphService,
  parseMemoryNodeId,
  parseRelatedSkills,
  type LearningGraphDeps,
} from "../src/index.js";
import type {
  SkillRegistryPort,
  SkillUsagePort,
  SkillProvenancePort,
  SkillSummary,
  SkillUsageRecord,
  SkillReadResult,
} from "../src/index.js";
import type { MemoryStorePort, MemorySnapshot } from "../src/index.js";

function fakeSummary(id: string, overrides: Partial<SkillSummary> = {}): SkillSummary {
  return { id, name: id, description: "test", fileCount: 1, updatedAt: "2025-06-01T00:00:00Z", ...overrides };
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

function makeDeps(overrides: Partial<LearningGraphDeps> = {}): LearningGraphDeps {
  return {
    registry: {
      list: async () => [],
      read: async () => ({ skillId: "", path: "", sizeBytes: 0, editable: false, truncated: false }) as SkillReadResult,
    } as unknown as SkillRegistryPort,
    usage: {
      listRecords: async () => [],
      getRecord: async () => fakeUsageRecord("x"),
    } as unknown as SkillUsagePort,
    provenance: {
      get: async () => "user",
    } as unknown as SkillProvenancePort,
    memoryStore: {
      loadSnapshot: async () => fakeSnapshot(),
    } as unknown as MemoryStorePort,
    ...overrides,
  };
}

describe("LearningGraphService.buildGraph", () => {
  it("produces correct node id formats", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("coding/refactor")],
        read: async () => ({ skillId: "coding/refactor", path: "SKILL.md", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("coding/refactor", { useCount: 3 })],
        getRecord: async () => fakeUsageRecord("coding/refactor", { useCount: 3 }),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["hello", "world"], ["user note"]),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    const skillNode = graph.nodes.find((n) => n.kind === "skill");
    expect(skillNode?.id).toBe("coding/refactor");

    const memNodes = graph.nodes.filter((n) => n.kind === "memory");
    expect(memNodes[0]?.id).toBe("memory:memory:0");
    expect(memNodes[1]?.id).toBe("memory:memory:1");
    expect(memNodes[2]?.id).toBe("memory:user:2");
  });

  it("includes agent-created skills and skills with usage activity, excludes others", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [
          fakeSummary("agent-skill"),
          fakeSummary("used-user-skill"),
          fakeSummary("inactive-user-skill"),
        ],
        read: async () => ({ skillId: "", path: "", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [
          fakeUsageRecord("agent-skill", { useCount: 0 }),
          fakeUsageRecord("used-user-skill", { useCount: 5 }),
          fakeUsageRecord("inactive-user-skill", { useCount: 0 }),
        ],
        getRecord: async (id: string) => fakeUsageRecord(id),
      } as unknown as SkillUsagePort,
      provenance: {
        get: async (id: string) => (id === "agent-skill" ? "agent" : "user"),
      } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    const skillIds = graph.nodes.filter((n) => n.kind === "skill").map((n) => n.id);
    expect(skillIds).toContain("agent-skill");
    expect(skillIds).toContain("used-user-skill");
    expect(skillIds).not.toContain("inactive-user-skill");
  });

  it("keeps builtin skills out of Learning even when they have usage activity", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("skill-creator"), fakeSummary("agent-skill")],
        read: async () => ({ skillId: "", path: "", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [
          fakeUsageRecord("skill-creator", { useCount: 8, viewCount: 3 }),
          fakeUsageRecord("agent-skill", { useCount: 1 }),
        ],
        getRecord: async (id: string) => fakeUsageRecord(id),
      } as unknown as SkillUsagePort,
      provenance: {
        get: async (id: string) => id === "skill-creator" ? "builtin" : "agent",
      } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const graph = await new LearningGraphService(deps).buildGraph();
    const skillIds = graph.nodes.filter((node) => node.kind === "skill").map((node) => node.id);

    expect(skillIds).not.toContain("skill-creator");
    expect(skillIds).toContain("agent-skill");
  });

  it("excludes archived skills from the graph", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("archived-skill"), fakeSummary("active-skill")],
        read: async () => ({ skillId: "", path: "", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [
          fakeUsageRecord("archived-skill", { state: "archived", useCount: 10 }),
          fakeUsageRecord("active-skill", { state: "active", useCount: 2 }),
        ],
        getRecord: async (id: string) => fakeUsageRecord(id),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    const skillIds = graph.nodes.filter((n) => n.kind === "skill").map((n) => n.id);
    expect(skillIds).not.toContain("archived-skill");
    expect(skillIds).toContain("active-skill");
  });

  it("only includes edges when both endpoints exist in the graph", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("alpha"), fakeSummary("beta"), fakeSummary("gamma")],
        read: async (id: string) => ({
          skillId: id,
          path: "SKILL.md",
          content: id === "alpha"
            ? "---\nrelated_skills:\n  - beta\n  - delta\n---\n# Alpha"
            : "---\n---\n# " + id,
          sizeBytes: 0,
          editable: false,
          truncated: false,
        }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [
          fakeUsageRecord("alpha", { useCount: 1 }),
          fakeUsageRecord("beta", { useCount: 1 }),
          fakeUsageRecord("gamma", { useCount: 1 }),
        ],
        getRecord: async (id: string) => fakeUsageRecord(id),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: { loadSnapshot: async () => fakeSnapshot() } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    expect(graph.edges).toContainEqual({ source: "alpha", target: "beta" });
    expect(graph.edges).not.toContainEqual({ source: "alpha", target: "delta" });
  });

  it("uses memory createdAt when present and null for legacy undated entries", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("skill-a", { updatedAt: "2025-06-01T00:00:00Z" })],
        read: async () => ({ skillId: "", path: "", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [fakeUsageRecord("skill-a", { useCount: 1, lastUsedAt: "2025-06-15T00:00:00Z" })],
        getRecord: async () => fakeUsageRecord("skill-a", { useCount: 1 }),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: {
        loadSnapshot: async () => ({
          memory: [
            { text: "legacy", createdAt: null },
            { text: "dated", createdAt: "2026-08-02T12:00:00.000Z" },
          ],
          user: [],
          usage: {
            memory: { chars: 12, limit: 2200 },
            user: { chars: 0, limit: 1375 },
          },
        }),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    const skillNode = graph.nodes.find((n) => n.kind === "skill")!;
    const legacy = graph.nodes.find((n) => n.label === "legacy")!;
    const dated = graph.nodes.find((n) => n.label === "dated")!;
    expect(skillNode.timestamp).toBe(new Date("2025-06-15T00:00:00Z").getTime());
    expect(legacy.timestamp).toBeNull();
    expect(dated.timestamp).toBe(new Date("2026-08-02T12:00:00.000Z").getTime());
  });

  it("builds clusters and stats correctly", async () => {
    const deps = makeDeps({
      registry: {
        list: async () => [fakeSummary("coding/a"), fakeSummary("coding/b"), fakeSummary("writing/c")],
        read: async () => ({ skillId: "", path: "", content: "", sizeBytes: 0, editable: false, truncated: false }),
      } as unknown as SkillRegistryPort,
      usage: {
        listRecords: async () => [
          fakeUsageRecord("coding/a", { useCount: 2 }),
          fakeUsageRecord("coding/b", { useCount: 0 }),
          fakeUsageRecord("writing/c", { useCount: 1 }),
        ],
        getRecord: async (id: string) => fakeUsageRecord(id),
      } as unknown as SkillUsagePort,
      provenance: { get: async () => "agent" } as unknown as SkillProvenancePort,
      memoryStore: {
        loadSnapshot: async () => fakeSnapshot(["note1", "note2"], ["user1"]),
      } as unknown as MemoryStorePort,
    });
    const service = new LearningGraphService(deps);
    const graph = await service.buildGraph();

    const codingCluster = graph.clusters.find((c) => c.category === "coding");
    expect(codingCluster?.count).toBe(2);

    expect(graph.stats.skills).toBe(3);
    expect(graph.stats.learnedSkills).toBe(3);
    expect(graph.stats.memoryNodes).toBe(3);
    expect(graph.stats.agentCreated).toBe(3);
    expect(graph.stats.used).toBe(2);
  });
});

describe("parseMemoryNodeId", () => {
  it("parses valid memory node ids", () => {
    expect(parseMemoryNodeId("memory:memory:0")).toEqual({ source: "memory", index: 0 });
    expect(parseMemoryNodeId("memory:user:3")).toEqual({ source: "user", index: 3 });
  });

  it("rejects invalid ids", () => {
    expect(parseMemoryNodeId("skill-id")).toBeNull();
    expect(parseMemoryNodeId("memory:bad:0")).toBeNull();
    expect(parseMemoryNodeId("memory:memory:-1")).toBeNull();
    expect(parseMemoryNodeId("memory:memory:abc")).toBeNull();
  });
});

describe("parseRelatedSkills", () => {
  it("parses YAML list format", () => {
    const content = "---\nrelated_skills:\n  - alpha\n  - beta\n---\n# Skill";
    expect(parseRelatedSkills(content)).toEqual(["alpha", "beta"]);
  });

  it("parses inline array format", () => {
    const content = "---\nrelated_skills: [alpha, beta]\n---\n# Skill";
    expect(parseRelatedSkills(content)).toEqual(["alpha", "beta"]);
  });

  it("returns empty when no frontmatter", () => {
    expect(parseRelatedSkills("# No frontmatter")).toEqual([]);
  });

  it("returns empty when no related_skills key", () => {
    const content = "---\nname: test\n---\n# Skill";
    expect(parseRelatedSkills(content)).toEqual([]);
  });
});
