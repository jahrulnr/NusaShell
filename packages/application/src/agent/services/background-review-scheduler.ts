import { randomUUID } from "node:crypto";
import type { AgentMessage } from "../ports/agent-provider.port.js";
import type { AgentProviderRegistryPort } from "../ports/agent-provider.port.js";
import type { PromptLoaderPort, ReviewPromptKind } from "../ports/prompt-loader.port.js";
import type { ReviewState, ReviewStateStorePort } from "../ports/review-state-store.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AgentTurnRunner, AgentTurnResult } from "./agent-turn-runner.js";
import type { ReviewAgentToolGateway } from "./review-agent-tool-gateway.js";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import { createLearningUpdatedEvent } from "../../events/agent-learning-updated.event.js";

export interface BackgroundReviewSettings {
  readonly enabled: boolean;
  readonly memoryEveryNTurns: number;
  readonly skillEveryNToolRounds: number;
  readonly maxToolRounds: number;
  readonly transcriptTailMessages: number;
}

export const DEFAULT_REVIEW_SETTINGS: BackgroundReviewSettings = {
  enabled: true,
  memoryEveryNTurns: 10,
  skillEveryNToolRounds: 10,
  maxToolRounds: 6,
  transcriptTailMessages: 40,
};

export interface BackgroundReviewSchedulerDeps {
  readonly stateStore: ReviewStateStorePort;
  readonly promptLoader: PromptLoaderPort;
  readonly providerRegistry: AgentProviderRegistryPort;
  readonly reviewGateway: ReviewAgentToolGateway;
  readonly runnerFactory: (deps: {
    readonly provider: AgentProviderRegistryPort["get"] extends (id: string) => infer P | undefined ? NonNullable<P> : never;
    readonly toolGateway: ReviewAgentToolGateway;
    readonly maxToolRounds: number;
  }) => AgentTurnRunner;
  readonly defaultProviderId: string;
  readonly eventDispatcher: EventDispatcher;
  readonly logger?: LoggerPort;
}

/**
 * After each successful parent turn, ticks counters and fire-and-forget
 * spawns a restricted review turn when thresholds are crossed. Never
 * blocks or fails the parent turn.
 */
export class BackgroundReviewScheduler {
  private settings: BackgroundReviewSettings = DEFAULT_REVIEW_SETTINGS;
  private reviewInFlight = false;

  constructor(private readonly deps: BackgroundReviewSchedulerDeps) {}

  configure(settings: Partial<BackgroundReviewSettings>): void {
    this.settings = { ...this.settings, ...settings };
  }

  getSettings(): BackgroundReviewSettings {
    return this.settings;
  }

  async tick(parentResult: AgentTurnResult): Promise<void> {
    if (!this.settings.enabled) return;
    if (this.reviewInFlight) return;

    let state = await this.deps.stateStore.load();
    state = {
      ...state,
      turnsSinceMemory: state.turnsSinceMemory + 1,
      toolRoundsSinceSkill: state.toolRoundsSinceSkill + parentResult.rounds,
    };

    const reviewMemory = state.turnsSinceMemory >= this.settings.memoryEveryNTurns;
    const reviewSkills = state.toolRoundsSinceSkill >= this.settings.skillEveryNToolRounds;

    if (!reviewMemory && !reviewSkills) {
      await this.deps.stateStore.save(state);
      return;
    }

    if (reviewMemory) state = { ...state, turnsSinceMemory: 0 };
    if (reviewSkills) state = { ...state, toolRoundsSinceSkill: 0 };
    state = { ...state, lastReviewAt: new Date().toISOString() };
    await this.deps.stateStore.save(state);

    void this.spawnReview(parentResult, reviewMemory, reviewSkills);
  }

  private async spawnReview(
    parentResult: AgentTurnResult,
    reviewMemory: boolean,
    reviewSkills: boolean,
  ): Promise<void> {
    this.reviewInFlight = true;
    try {
      const provider = this.deps.providerRegistry.get(this.deps.defaultProviderId);
      if (!provider) {
        this.deps.logger?.warn("Background review skipped: provider not found");
        return;
      }

      const kind: ReviewPromptKind = reviewMemory && reviewSkills
        ? "combined"
        : reviewMemory
          ? "memory"
          : "skill";

      const systemPrompt = await this.deps.promptLoader.loadReviewPrompt(kind);
      const transcript = this.buildTranscript(parentResult);

      const runner = this.deps.runnerFactory({
        provider,
        toolGateway: this.deps.reviewGateway,
        maxToolRounds: this.settings.maxToolRounds,
      });

      const reviewTraceId = randomUUID();
      this.deps.logger?.info("Background review spawned traceId=%s kind=%s", reviewTraceId, kind);

      const result = await runner.run({
        messages: [
          { role: "system", content: systemPrompt },
          { role: "user", content: transcript },
        ],
        pluginIds: [],
        traceId: reviewTraceId,
        maxToolRounds: this.settings.maxToolRounds,
      });

      const mutations = this.detectMutations(result);
      if (mutations.length > 0) {
        const summary = result.text?.trim() || `Review updated: ${mutations.join(", ")}`;
        await this.deps.eventDispatcher.publish(
          createLearningUpdatedEvent(reviewTraceId, mutations, summary),
        );
        this.deps.logger?.info("Background review completed traceId=%s mutations=%s", reviewTraceId, mutations.join(","));
      } else {
        this.deps.logger?.info("Background review completed traceId=%s no mutations", reviewTraceId);
      }
    } catch (error) {
      this.deps.logger?.error("Background review failed: %s", error instanceof Error ? error.message : String(error));
    } finally {
      this.reviewInFlight = false;
    }
  }

  private buildTranscript(parentResult: AgentTurnResult): string {
    const messages = parentResult.messages ?? [];
    const tail = messages.slice(-this.settings.transcriptTailMessages);
    const lines: string[] = [];
    for (const msg of tail) {
      const role = msg.role;
      let content: string;
      if (typeof msg.content === "string") {
        content = msg.content;
      } else if (Array.isArray(msg.content)) {
        content = msg.content.map((p) => (typeof p === "string" ? p : JSON.stringify(p))).join("");
      } else {
        content = JSON.stringify(msg.content ?? "");
      }
      const truncated = content.length > 4000 ? content.slice(0, 4000) + "…[truncated]" : content;
      lines.push(`[${role}] ${truncated}`);
    }
    return lines.join("\n\n");
  }

  private detectMutations(result: AgentTurnResult): string[] {
    const kinds = new Set<string>();
    for (const call of result.toolCalls) {
      if (call.name === "memory" && call.ok) kinds.add("memory");
      if (call.name === "skill_manage" && call.ok) kinds.add("skills");
    }
    return [...kinds];
  }
}
