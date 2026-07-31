import type { SkillRegistryPort, SkillProvenancePort, SkillUsagePort, SkillState, SkillSummary } from "./index.js";
import { latestActivityAt } from "./index.js";
import type { EventDispatcher } from "../events/event-dispatcher.js";
import { createLearningUpdatedEvent } from "../events/agent-learning-updated.event.js";
import type { LoggerPort } from "../plugin/ports/logger.port.js";

export interface CuratorSettings {
  readonly enabled: boolean;
  readonly staleAfterDays: number;
  readonly archiveAfterDays: number;
  readonly pruneUserOwned: boolean;
}

export const DEFAULT_CURATOR_SETTINGS: CuratorSettings = {
  enabled: true,
  staleAfterDays: 30,
  archiveAfterDays: 90,
  pruneUserOwned: false,
};

export interface CuratorChange {
  readonly skillId: string;
  readonly from: SkillState;
  readonly to: SkillState;
}

export interface CuratorResult {
  readonly dryRun: boolean;
  readonly changes: readonly CuratorChange[];
}

export interface SkillCuratorDeps {
  readonly registry: SkillRegistryPort;
  readonly provenance: SkillProvenancePort;
  readonly usage: SkillUsagePort;
  readonly eventDispatcher?: EventDispatcher;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
}

const MS_PER_DAY = 24 * 60 * 60 * 1000;

export class SkillCuratorService {
  private settings: CuratorSettings = DEFAULT_CURATOR_SETTINGS;

  constructor(private readonly deps: SkillCuratorDeps) {}

  configure(settings: Partial<CuratorSettings>): void {
    this.settings = { ...this.settings, ...settings };
  }

  getSettings(): CuratorSettings {
    return this.settings;
  }

  async run(dryRun = false): Promise<CuratorResult> {
    if (!this.settings.enabled) {
      return { dryRun, changes: [] };
    }
    const now = (this.deps.now ?? (() => new Date()))();
    const skills = await this.deps.registry.list();
    const changes: CuratorChange[] = [];

    for (const skill of skills) {
      const change = await this.evaluateSkill(skill, now, dryRun);
      if (change) changes.push(change);
    }

    if (!dryRun && changes.length > 0 && this.deps.eventDispatcher) {
      const summary = changes.map((c) => `${c.skillId}: ${c.from}→${c.to}`).join(", ");
      void this.deps.eventDispatcher.publish(
        createLearningUpdatedEvent(`curator-${now.toISOString()}`, ["skill_curator"], summary, now),
      );
    }

    return { dryRun, changes };
  }

  private async evaluateSkill(skill: SkillSummary, now: Date, dryRun: boolean): Promise<CuratorChange | null> {
    const origin = await this.deps.provenance.get(skill.id);
    if (origin !== "agent" && !this.settings.pruneUserOwned) return null;

    const usage = await this.deps.usage.getRecord(skill.id);
    if (usage.pinned) return null;

    const currentState = usage.state;
    const lastActivity = new Date(latestActivityAt(usage));
    const daysSinceActivity = (now.getTime() - lastActivity.getTime()) / MS_PER_DAY;

    let nextState: SkillState | null = null;
    if (currentState === "active" && daysSinceActivity >= this.settings.staleAfterDays) {
      nextState = "stale";
    } else if (currentState === "stale" && daysSinceActivity >= this.settings.archiveAfterDays) {
      nextState = "archived";
    } else if (currentState === "active" && daysSinceActivity >= this.settings.archiveAfterDays) {
      nextState = "archived";
    }

    if (!nextState || nextState === currentState) return null;

    if (dryRun) {
      return { skillId: skill.id, from: currentState, to: nextState };
    }

    if (nextState === "archived") {
      await this.deps.registry.archive(skill.id);
    }
    await this.deps.usage.setState(skill.id, nextState);

    return { skillId: skill.id, from: currentState, to: nextState };
  }
}
