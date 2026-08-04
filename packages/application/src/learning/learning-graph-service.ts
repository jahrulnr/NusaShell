import type {
  SkillRegistryPort,
  SkillUsagePort,
  SkillProvenancePort,
  SkillSummary,
  SkillUsageRecord,
  SkillReadResult,
} from "../skill/index.js";
import { latestActivityAt } from "../skill/index.js";
import type { MemoryStorePort, MemorySnapshot, MemoryEntry, MemoryTarget } from "../memory/ports/memory-store.port.js";

export interface LearningNode {
  readonly id: string;
  readonly label: string;
  readonly kind: "skill" | "memory";
  readonly timestamp: number | null;
  readonly category: string;
  readonly useCount: number;
  readonly state: "active" | "stale" | "archived";
  readonly createdBy: "agent" | "user" | "builtin";
  readonly pinned: boolean;
  readonly memorySource?: "memory" | "user";
}

export interface LearningEdge {
  readonly source: string;
  readonly target: string;
}

export interface LearningCluster {
  readonly category: string;
  readonly count: number;
}

export interface LearningGraphStats {
  readonly skills: number;
  readonly learnedSkills: number;
  readonly memoryNodes: number;
  readonly agentCreated: number;
  readonly used: number;
}

export interface LearningGraph {
  readonly nodes: readonly LearningNode[];
  readonly edges: readonly LearningEdge[];
  readonly clusters: readonly LearningCluster[];
  readonly stats: LearningGraphStats;
}

export interface LearningNodeDetail {
  readonly id: string;
  readonly kind: "skill" | "memory";
  readonly label: string;
  readonly content: string;
  readonly editable: boolean;
  readonly memorySource?: "memory" | "user";
}

export interface MutationResult {
  readonly ok: boolean;
  readonly error?: string;
  readonly code?: string;
}

export interface LearningGraphDeps {
  readonly registry: SkillRegistryPort;
  readonly usage: SkillUsagePort;
  readonly provenance: SkillProvenancePort;
  readonly memoryStore: MemoryStorePort;
}

const SKILL_MD = "SKILL.md";

export class LearningGraphService {
  constructor(private readonly deps: LearningGraphDeps) {}

  async buildGraph(): Promise<LearningGraph> {
    const [skills, usageRecords, memorySnapshot] = await Promise.all([
      this.deps.registry.list(),
      this.deps.usage.listRecords(),
      this.deps.memoryStore.loadSnapshot(),
    ]);

    const usageMap = new Map<string, SkillUsageRecord>();
    for (const record of usageRecords) {
      usageMap.set(record.skillId, record);
    }

    const nodes: LearningNode[] = [];
    const skillIds = new Set<string>();

    for (const skill of skills) {
      const usage = usageMap.get(skill.id);
      const origin = await this.deps.provenance.get(skill.id);
      const state = usage?.state ?? "active";
      const pinned = usage?.pinned ?? false;

      // Builtins are shell capabilities, not user learning. Usage activity
      // must not pull them into this review surface (for example skill-creator).
      if (origin === "builtin") continue;
      if (state === "archived") continue;

      const isAgent = origin === "agent";
      const hasActivity = this.hasUsageActivity(usage);

      if (!isAgent && !hasActivity) continue;

      const timestamp = this.skillTimestamp(usage, skill);
      const useCount = usage?.useCount ?? 0;

      nodes.push({
        id: skill.id,
        label: skill.name,
        kind: "skill",
        timestamp,
        category: this.skillCategory(skill),
        useCount,
        state,
        createdBy: origin,
        pinned,
      });
      skillIds.add(skill.id);
    }

    this.appendMemoryNodes(nodes, memorySnapshot);

    const edges = await this.buildEdges(skills, skillIds);
    const clusters = this.buildClusters(nodes);
    const stats = this.buildStats(nodes);

    return { nodes, edges, clusters, stats };
  }

  async getNode(nodeId: string): Promise<LearningNodeDetail> {
    if (nodeId.startsWith("memory:")) {
      return this.getMemoryNodeDetail(nodeId);
    }
    return this.getSkillNodeDetail(nodeId);
  }

  async editNode(nodeId: string, content: string): Promise<MutationResult> {
    if (nodeId.startsWith("memory:")) {
      return this.editMemoryNode(nodeId, content);
    }
    return this.editSkillNode(nodeId, content);
  }

  async deleteNode(nodeId: string): Promise<MutationResult> {
    if (nodeId.startsWith("memory:")) {
      return this.deleteMemoryNode(nodeId);
    }
    return this.deleteSkillNode(nodeId);
  }

  private hasUsageActivity(usage: SkillUsageRecord | undefined): boolean {
    if (!usage) return false;
    return usage.useCount > 0 || usage.viewCount > 0 || usage.patchCount > 0;
  }

  private skillTimestamp(usage: SkillUsageRecord | undefined, skill: SkillSummary): number | null {
    if (usage) {
      const latest = latestActivityAt(usage);
      return new Date(latest).getTime();
    }
    return new Date(skill.updatedAt).getTime();
  }

  private skillCategory(skill: SkillSummary): string {
    const parts = skill.id.split("/");
    return parts.length > 1 ? parts[0]! : "general";
  }

  private appendMemoryNodes(nodes: LearningNode[], snapshot: MemorySnapshot): void {
    let globalIndex = 0;
    for (const entry of snapshot.memory) {
      nodes.push(this.makeMemoryNode(entry, "memory", globalIndex));
      globalIndex++;
    }
    for (const entry of snapshot.user) {
      nodes.push(this.makeMemoryNode(entry, "user", globalIndex));
      globalIndex++;
    }
  }

  private makeMemoryNode(
    entry: MemoryEntry,
    source: MemoryTarget,
    index: number,
  ): LearningNode {
    const id = `memory:${source}:${index}`;
    const label = entry.text.length > 60 ? `${entry.text.slice(0, 57)}…` : entry.text;
    const timestamp = entry.createdAt ? new Date(entry.createdAt).getTime() : null;
    return {
      id,
      label,
      kind: "memory",
      timestamp: timestamp !== null && !Number.isNaN(timestamp) ? timestamp : null,
      category: source === "memory" ? "memory" : "user",
      useCount: 0,
      state: "active",
      createdBy: "agent",
      pinned: false,
      memorySource: source,
    };
  }

  private async buildEdges(
    skills: readonly SkillSummary[],
    skillIds: Set<string>,
  ): Promise<LearningEdge[]> {
    const edges: LearningEdge[] = [];
    for (const skill of skills) {
      if (!skillIds.has(skill.id)) continue;
      const related = await this.extractRelatedSkills(skill.id);
      for (const target of related) {
        if (skillIds.has(target) && target !== skill.id) {
          edges.push({ source: skill.id, target });
        }
      }
    }
    return edges;
  }

  private async extractRelatedSkills(skillId: string): Promise<string[]> {
    try {
      const result: SkillReadResult = await this.deps.registry.read(skillId, SKILL_MD);
      if (!result.content) return [];
      return parseRelatedSkills(result.content);
    } catch {
      return [];
    }
  }

  private buildClusters(nodes: readonly LearningNode[]): LearningCluster[] {
    const counts = new Map<string, number>();
    for (const node of nodes) {
      counts.set(node.category, (counts.get(node.category) ?? 0) + 1);
    }
    return [...counts.entries()]
      .map(([category, count]) => ({ category, count }))
      .sort((a, b) => b.count - a.count);
  }

  private buildStats(nodes: readonly LearningNode[]): LearningGraphStats {
    let skills = 0;
    let learnedSkills = 0;
    let memoryNodes = 0;
    let agentCreated = 0;
    let used = 0;

    for (const node of nodes) {
      if (node.kind === "skill") {
        skills++;
        if (node.createdBy === "agent") {
          learnedSkills++;
          agentCreated++;
        }
        if (node.useCount > 0) used++;
      } else {
        memoryNodes++;
      }
    }

    return { skills, learnedSkills, memoryNodes, agentCreated, used };
  }

  private async getSkillNodeDetail(skillId: string): Promise<LearningNodeDetail> {
    const detail = await this.deps.registry.get(skillId);
    let content = "";
    let editable = false;
    try {
      const result = await this.deps.registry.read(skillId, SKILL_MD);
      content = result.content ?? "";
      editable = result.editable;
    } catch {
      editable = false;
    }
    return {
      id: skillId,
      kind: "skill",
      label: detail.name,
      content,
      editable,
    };
  }

  private async getMemoryNodeDetail(nodeId: string): Promise<LearningNodeDetail> {
    const parsed = parseMemoryNodeId(nodeId);
    if (!parsed) {
      return { id: nodeId, kind: "memory", label: "Unknown", content: "", editable: false };
    }
    const snapshot = await this.deps.memoryStore.loadSnapshot();
    const localIndex = this.globalToLocalIndex(parsed, snapshot);
    const entries = parsed.source === "memory" ? snapshot.memory : snapshot.user;
    const entry = entries[localIndex];
    return {
      id: nodeId,
      kind: "memory",
      label: entry ? truncate(entry.text, 60) : "Entry not found",
      content: entry?.text ?? "",
      editable: true,
      memorySource: parsed.source,
    };
  }

  private async editSkillNode(skillId: string, content: string): Promise<MutationResult> {
    try {
      await this.deps.registry.write(skillId, SKILL_MD, content);
      return { ok: true };
    } catch (err) {
      return { ok: false, error: errorMessage(err), code: "write_failed" };
    }
  }

  private async editMemoryNode(nodeId: string, content: string): Promise<MutationResult> {
    const parsed = parseMemoryNodeId(nodeId);
    if (!parsed) return { ok: false, error: "Invalid memory node id", code: "invalid_id" };

    const trimmed = content.trim();
    if (trimmed.length === 0) {
      return { ok: false, error: "Empty content — use delete to remove an entry", code: "empty_content" };
    }

    const snapshot = await this.deps.memoryStore.loadSnapshot();
    const localIndex = this.globalToLocalIndex(parsed, snapshot);
    const entries = parsed.source === "memory" ? snapshot.memory : snapshot.user;
    const entry = entries[localIndex];
    if (!entry) {
      return { ok: false, error: "Entry no longer exists — refresh the graph", code: "node_stale" };
    }

    try {
      await this.deps.memoryStore.replace(parsed.source, entry.text, trimmed);
      return { ok: true };
    } catch (err) {
      const msg = errorMessage(err);
      if (msg.includes("did not match") || msg.includes("multiple")) {
        return { ok: false, error: "Entry no longer matches — refresh the graph", code: "node_stale" };
      }
      return { ok: false, error: msg, code: "replace_failed" };
    }
  }

  private async deleteSkillNode(skillId: string): Promise<MutationResult> {
    try {
      const origin = await this.deps.provenance.get(skillId);
      const usage = await this.deps.usage.getRecord(skillId);
      if (usage.pinned) {
        return {
          ok: false,
          error: "Cannot archive a pinned skill — unpin it first",
          code: "pinned",
        };
      }
      // Builtin skills are seeded from resources on every startup. Archiving
      // alone is not enough — the seeder would resurrect the skill on the next
      // launch. Mark it as intentionally deleted so the seeder skips it.
      if (origin === "builtin") {
        if (this.deps.provenance.markBuiltinDeleted) {
          await this.deps.provenance.markBuiltinDeleted(skillId);
        }
        await this.deps.registry.archive(skillId);
        return { ok: true };
      }
      await this.deps.registry.archive(skillId);
      return { ok: true };
    } catch (err) {
      return { ok: false, error: errorMessage(err), code: "archive_failed" };
    }
  }

  private globalToLocalIndex(
    parsed: { source: MemoryTarget; index: number },
    snapshot: MemorySnapshot,
  ): number {
    if (parsed.source === "memory") return parsed.index;
    return parsed.index - snapshot.memory.length;
  }

  private async deleteMemoryNode(nodeId: string): Promise<MutationResult> {
    const parsed = parseMemoryNodeId(nodeId);
    if (!parsed) return { ok: false, error: "Invalid memory node id", code: "invalid_id" };

    const snapshot = await this.deps.memoryStore.loadSnapshot();
    const localIndex = this.globalToLocalIndex(parsed, snapshot);
    const entries = parsed.source === "memory" ? snapshot.memory : snapshot.user;
    const entry = entries[localIndex];
    if (!entry) {
      return { ok: false, error: "Entry no longer exists — refresh the graph", code: "node_stale" };
    }

    try {
      await this.deps.memoryStore.remove(parsed.source, entry.text);
      return { ok: true };
    } catch (err) {
      const msg = errorMessage(err);
      if (msg.includes("did not match") || msg.includes("multiple")) {
        return { ok: false, error: "Entry no longer matches — refresh the graph", code: "node_stale" };
      }
      return { ok: false, error: msg, code: "remove_failed" };
    }
  }
}

export function parseMemoryNodeId(nodeId: string): { source: MemoryTarget; index: number } | null {
  const parts = nodeId.split(":");
  if (parts.length !== 3 || parts[0] !== "memory") return null;
  const source = parts[1] as MemoryTarget;
  if (source !== "memory" && source !== "user") return null;
  const index = Number(parts[2]);
  if (!Number.isInteger(index) || index < 0) return null;
  return { source, index };
}

export function parseRelatedSkills(content: string): string[] {
  const fm = extractFrontmatter(content);
  if (!fm) return [];
  const match = fm.match(/^related_skills:\s*\r?\n((?:\s*-\s+.+\r?\n?)+)/m);
  if (!match) {
    const inline = fm.match(/^related_skills:\s*\[(.+?)\]/m);
    if (!inline) return [];
    return inline[1]!
      .split(",")
      .map((s) => s.trim().replace(/^["']|["']$/g, ""))
      .filter((s) => s.length > 0);
  }
  const lines = match[1]!.split(/\r?\n/).filter((l) => l.trim().startsWith("-"));
  return lines
    .map((l) => l.replace(/^\s*-\s*/, "").trim().replace(/^["']|["']$/g, ""))
    .filter((s) => s.length > 0);
}

function extractFrontmatter(content: string): string | null {
  if (!content.startsWith("---")) return null;
  const endMatch = content.slice(3).match(/\r?\n---/);
  if (!endMatch) return null;
  const end = endMatch.index! + 3;
  return content.slice(3, end);
  if (end === -1) return null;
  return content.slice(3, end);
}

function truncate(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
