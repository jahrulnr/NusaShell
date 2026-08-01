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
