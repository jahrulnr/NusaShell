import {
  FilesystemSkillRegistry,
  FilesystemSkillProvenance,
  FilesystemSkillUsage,
  FilesystemCuratorStateStore,
  SkillApprovalStaging,
  FilesystemMemoryStore,
  type Logger,
} from "@nusashell/infrastructure";
import {
  SkillCuratorService,
  SkillCuratorScheduler,
  LearningGraphService,
  type EventDispatcher,
  type SkillRegistryPort,
  type SkillProvenancePort,
  type SkillUsagePort,
  type MemoryStorePort,
} from "@nusashell/application";
import { fileURLToPath } from "node:url";
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { ContainerOptions } from "../container.js";

export interface SkillsRuntimeParts {
  readonly skillRegistry: SkillRegistryPort;
  readonly skillProvenance: SkillProvenancePort;
  readonly skillUsage: SkillUsagePort;
  readonly skillApprovalStaging: SkillApprovalStaging;
  readonly skillCurator: SkillCuratorService;
  readonly skillCuratorScheduler: SkillCuratorScheduler;
  readonly memoryStore: MemoryStorePort;
  readonly learningGraph: LearningGraphService;
}

export function createSkillsRuntime(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
): SkillsRuntimeParts {
  const skillsRoot = options.skillsRoot ?? fileURLToPath(new URL("../../../.nusashell/agent/skills", import.meta.url));
  ensureBuiltinSkill(options.builtinSkillsRoot, skillsRoot, "mcp-creator");
  ensureBuiltinSkill(options.builtinSkillsRoot, skillsRoot, "skill-creator");
  const skillRegistry = new FilesystemSkillRegistry(skillsRoot);
  const skillProvenance = new FilesystemSkillProvenance(skillsRoot);
  const skillUsage = new FilesystemSkillUsage(skillsRoot);
  const skillApprovalStaging = new SkillApprovalStaging(skillsRoot);
  const skillCurator = new SkillCuratorService({
    registry: skillRegistry,
    provenance: skillProvenance,
    usage: skillUsage,
    eventDispatcher,
    logger,
  });
  const skillCuratorScheduler = new SkillCuratorScheduler({
    curator: skillCurator,
    stateStore: new FilesystemCuratorStateStore(skillsRoot),
    logger,
  });
  void skillCuratorScheduler.initialize().catch((err) => {
    logger.warn({ err }, "Skill curator scheduler initialization failed");
  });

  const memoryRoot = options.memoryRoot ?? fileURLToPath(new URL("../../../.nusashell/agent/memory", import.meta.url));
  const memoryStore = new FilesystemMemoryStore(memoryRoot);
  const learningGraph = new LearningGraphService({
    registry: skillRegistry,
    usage: skillUsage,
    provenance: skillProvenance,
    memoryStore,
  });

  return {
    skillRegistry, skillProvenance, skillUsage, skillApprovalStaging,
    skillCurator, skillCuratorScheduler, memoryStore, learningGraph,
  };
}

export function ensureBuiltinSkill(sourceRoot: string | undefined, skillsRoot: string, skillId: string): void {
  if (!sourceRoot) return;
  const source = resolve(sourceRoot, skillId);
  const destination = resolve(skillsRoot, skillId);
  const sourceVersionPath = resolve(source, "VERSION");
  if (!existsSync(sourceVersionPath)) return;
  const sourceVersion = readFileSync(sourceVersionPath, "utf8").trim();
  const destinationVersionPath = resolve(destination, "VERSION");
  const destinationExists = existsSync(resolve(destination, "SKILL.md"));
  let destinationOrigin = "";
  try {
    const provenance = JSON.parse(readFileSync(resolve(skillsRoot, ".provenance.json"), "utf8")) as Record<string, { createdBy?: string }>;
    destinationOrigin = provenance[skillId]?.createdBy ?? "";
  } catch {
    destinationOrigin = "";
  }
  if (destinationExists && destinationOrigin !== "builtin") return;
  if (destinationExists && existsSync(destinationVersionPath) && readFileSync(destinationVersionPath, "utf8").trim() === sourceVersion) return;
  mkdirSync(skillsRoot, { recursive: true });
  if (destinationExists) rmSync(destination, { recursive: true, force: true });
  cpSync(source, destination, { recursive: true });
  const provenancePath = resolve(skillsRoot, ".provenance.json");
  let provenance: Record<string, { createdBy: string; createdAt: string }> = {};
  try { provenance = JSON.parse(readFileSync(provenancePath, "utf8")) as typeof provenance; } catch {}
  provenance[skillId] = { createdBy: "builtin", createdAt: new Date().toISOString() };
  writeFileSync(provenancePath, JSON.stringify(provenance, null, 2), "utf8");
}
