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
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const SKILL_ID = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const DELETED_BUILTIN_FILE = ".deleted-builtin.json";

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
  seedBuiltinSkills(options.builtinSkillsRoot, skillsRoot);
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

/**
 * Scan `sourceRoot` for skill folders (directories matching the skill-id regex
 * that contain a `SKILL.md`) and seed each one into `skillsRoot` as a builtin
 * skill. Silently no-ops when `sourceRoot` is missing, unreadable, or empty.
 *
 * After seeding, removes orphan builtin skills from `skillsRoot` that no longer
 * exist in `sourceRoot` (e.g. a skill removed in a new NusaShell version).
 * Skills with non-builtin provenance (user/agent-owned) are always preserved.
 * Skills listed in `.deleted-builtin.json` (user-intentionally-deleted) are
 * never re-seeded.
 */
export function seedBuiltinSkills(sourceRoot: string | undefined, skillsRoot: string): void {
  if (!sourceRoot || !existsSync(sourceRoot)) return;
  let entries: import("node:fs").Dirent[];
  try {
    entries = readdirSync(sourceRoot, { withFileTypes: true });
  } catch {
    return;
  }
  const sourceSkillIds = new Set<string>();
  for (const entry of entries) {
    if (!entry.isDirectory() || !SKILL_ID.test(entry.name)) continue;
    const source = resolve(sourceRoot, entry.name);
    if (!existsSync(resolve(source, "SKILL.md"))) continue;
    sourceSkillIds.add(entry.name);
    try {
      ensureBuiltinSkill(sourceRoot, skillsRoot, entry.name);
    } catch {
      // Skip a single broken skill without aborting the whole seed.
    }
  }
  cleanupOrphanBuiltinSkills(skillsRoot, sourceSkillIds);
}

export function ensureBuiltinSkill(sourceRoot: string | undefined, skillsRoot: string, skillId: string): void {
  if (!sourceRoot) return;
  const source = resolve(sourceRoot, skillId);
  const destination = resolve(skillsRoot, skillId);
  if (!existsSync(resolve(source, "SKILL.md"))) return;
  // Do not re-seed a builtin skill the user intentionally deleted.
  const deletedBuiltin = loadDeletedBuiltin(skillsRoot);
  if (skillId in deletedBuiltin) return;
  const destinationExists = existsSync(resolve(destination, "SKILL.md"));
  let destinationOrigin = "";
  try {
    const provenance = JSON.parse(readFileSync(resolve(skillsRoot, ".provenance.json"), "utf8")) as Record<string, { createdBy?: string }>;
    destinationOrigin = provenance[skillId]?.createdBy ?? "";
  } catch {
    destinationOrigin = "";
  }
  if (destinationExists && destinationOrigin !== "builtin") return;
  mkdirSync(skillsRoot, { recursive: true });
  if (destinationExists) rmSync(destination, { recursive: true, force: true });
  cpSync(source, destination, { recursive: true });
  const provenancePath = resolve(skillsRoot, ".provenance.json");
  let provenance: Record<string, { createdBy: string; createdAt: string }> = {};
  try { provenance = JSON.parse(readFileSync(provenancePath, "utf8")) as typeof provenance; } catch {}
  provenance[skillId] = { createdBy: "builtin", createdAt: new Date().toISOString() };
  writeFileSync(provenancePath, JSON.stringify(provenance, null, 2), "utf8");
}

/**
 * Remove builtin-provenance skills from `skillsRoot` that are not in
 * `sourceSkillIds`. Skills with user/agent provenance are preserved.
 * Also cleans stale entries from `.deleted-builtin.json` for skills that
 * are no longer in source (the deletion record is no longer needed).
 */
function cleanupOrphanBuiltinSkills(skillsRoot: string, sourceSkillIds: ReadonlySet<string>): void {
  if (!existsSync(skillsRoot)) return;
  let destEntries: import("node:fs").Dirent[];
  try {
    destEntries = readdirSync(skillsRoot, { withFileTypes: true });
  } catch {
    return;
  }
  let provenance: Record<string, { createdBy?: string }> = {};
  try {
    provenance = JSON.parse(readFileSync(resolve(skillsRoot, ".provenance.json"), "utf8")) as typeof provenance;
  } catch {}
  for (const entry of destEntries) {
    if (!entry.isDirectory() || !SKILL_ID.test(entry.name)) continue;
    if (sourceSkillIds.has(entry.name)) continue;
    const origin = provenance[entry.name]?.createdBy ?? "";
    if (origin !== "builtin") continue;
    const dest = resolve(skillsRoot, entry.name);
    try {
      rmSync(dest, { recursive: true, force: true });
    } catch {
      // Skip if removal fails — do not abort the whole cleanup.
    }
    delete provenance[entry.name];
  }
  // Persist updated provenance (orphan entries removed).
  try {
    writeFileSync(resolve(skillsRoot, ".provenance.json"), JSON.stringify(provenance, null, 2), "utf8");
  } catch {}
  // Clean stale deletion records for skills no longer in source.
  const deletedBuiltin = loadDeletedBuiltin(skillsRoot);
  let changed = false;
  for (const id of Object.keys(deletedBuiltin)) {
    if (!sourceSkillIds.has(id)) {
      delete deletedBuiltin[id];
      changed = true;
    }
  }
  if (changed) saveDeletedBuiltin(skillsRoot, deletedBuiltin);
}

/**
 * Read the `.deleted-builtin.json` file synchronously. Returns an empty object
 * if the file does not exist or is unreadable.
 */
function loadDeletedBuiltin(skillsRoot: string): Record<string, { deletedAt: string }> {
  try {
    return JSON.parse(readFileSync(resolve(skillsRoot, DELETED_BUILTIN_FILE), "utf8")) as Record<string, { deletedAt: string }>;
  } catch {
    return {};
  }
}

/**
 * Write the `.deleted-builtin.json` file synchronously.
 */
function saveDeletedBuiltin(skillsRoot: string, data: Record<string, { deletedAt: string }>): void {
  mkdirSync(skillsRoot, { recursive: true });
  writeFileSync(resolve(skillsRoot, DELETED_BUILTIN_FILE), JSON.stringify(data, null, 2), "utf8");
}

/**
 * Mark a builtin skill as intentionally deleted by the user. After this, the
 * skill will not be re-seeded on restart. Call this before removing the skill
 * folder from `skillsRoot`.
 */
export function markBuiltinSkillDeleted(skillsRoot: string, skillId: string): void {
  const data = loadDeletedBuiltin(skillsRoot);
  data[skillId] = { deletedAt: new Date().toISOString() };
  saveDeletedBuiltin(skillsRoot, data);
}

/**
 * Remove a skill from the deleted-builtin list so it can be re-seeded on the
 * next startup. Used by "restore" / "undelete" flows.
 */
export function unmarkBuiltinSkillDeleted(skillsRoot: string, skillId: string): void {
  const data = loadDeletedBuiltin(skillsRoot);
  if (skillId in data) {
    delete data[skillId];
    saveDeletedBuiltin(skillsRoot, data);
  }
}
