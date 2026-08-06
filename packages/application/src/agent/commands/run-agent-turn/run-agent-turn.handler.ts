import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentProviderRegistryPort, AgentToolCall } from "../../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../../ports/agent-tool-gateway.port.js";
import type { SubagentPort } from "../../ports/subagent-port.js";
import type { PromptLoaderPort } from "../../ports/prompt-loader.port.js";
import type { ActiveTurnProjectionPort } from "../../ports/active-turn-projection.port.js";
import {
  AgentTurnRunner,
  type AgentContextOptions,
  type AgentTurnResult,
  type AgentTurnPartial,
  type AgentToolExecution,
  type AgentContextUpdate,
  type AgentTurnStep,
} from "../../services/agent-turn-runner.js";
import { injectPrompts, type PromptVars } from "../../services/prompt-injector.js";
import { detectRuntimeOs, type RuntimeOsProbe } from "../../services/runtime-os.js";
import { formatMemoryPrompt } from "../../services/memory-prompt-formatter.js";
import { formatTodoPrompt } from "../../services/todo-prompt-formatter.js";
import { buildSkillsCatalogPrompt } from "../../services/skills-catalog-formatter.js";
import { formatMcpLivePrompt } from "../../services/mcp-live-prompt-formatter.js";
import type { ConversationTodoPort } from "../../ports/conversation-todo.port.js";
import type { MemoryStorePort } from "../../../memory/ports/memory-store.port.js";
import type { SkillRegistryPort } from "../../../skill/ports/skill-registry.port.js";
import { InProcessAgentTurnWorker, type AgentTurnWorker } from "../../services/in-process-agent-turn-worker.js";
import type { RunAgentTurnCommand } from "./run-agent-turn.command.js";
import type { LoggerPort } from "../../../plugin/ports/logger.port.js";
import {
  RoutedAgentProvider,
  type AgentProviderStrategy,
} from "../../services/routed-agent-provider.js";
import { AgentTurnCoordinator } from "../../services/agent-turn-coordinator.js";
import { decideAutoContinue, normalizeMaxAutoContinues } from "../../services/auto-continue-policy.js";
import { randomUUID } from "node:crypto";

export interface AgentRuntimeSettings {
  maxToolRounds: number;
  maxRepeatedToolCalls: number;
  softRecoverAttempts: number;
  maxConcurrentToolCalls: number;
  strategy: AgentProviderStrategy;
  totalAttemptBudget: number;
  context?: AgentContextOptions;
  /** Outer multi-turn auto-continue budget (default 10, cap 100). */
  maxAutoContinues?: number;
}

export class RunAgentTurnHandler implements CommandHandler<RunAgentTurnCommand, AgentTurnResult> {
  private readonly supersededTraceIds = new Set<string>();
  /** Open streaming segment contents per conversation (token-level). */
  private readonly streamingBuffers = new Map<string, { kind: "text" | "reasoning"; content: string }>();
  /** Process-lifetime round-robin cursor shared across all turns (A2). */
  private readonly roundRobinCursor = { value: 0 };
  /** Date is session-stable; it must not churn the cacheable prompt prefix. */
  private readonly promptSessionDate = new Date().toISOString().slice(0, 10);

  constructor(
    private readonly providers: AgentProviderRegistryPort,
    private readonly toolGateway: AgentToolGateway,
    private readonly defaultProviderId: string,
    private readonly runtime: AgentRuntimeSettings,
    private readonly logger?: LoggerPort,
    private readonly coordinator: AgentTurnCoordinator = new AgentTurnCoordinator(),
    private readonly onTextDelta?: (traceId: string, delta: string) => void,
    private readonly onReasoningDelta?: (traceId: string, delta: string) => void,
    private readonly onToolCallStart?: (traceId: string, call: AgentToolCall) => void,
    private readonly onToolCallEnd?: (traceId: string, execution: AgentToolExecution) => void,
    private readonly onContextUpdate?: (traceId: string, update: AgentContextUpdate) => void,
    private readonly promptLoader?: PromptLoaderPort,
    private readonly userPrompt: string = "",
    private readonly memoryStore?: MemoryStorePort,
    private readonly onTurnComplete?: (result: AgentTurnResult, context?: { conversationId?: string; resume?: boolean }) => Promise<void> | void,
    private readonly onTurnEnd?: (traceId: string, reason: "completed" | "cancelled" | "failed" | "superseded") => void,
    private readonly onTurnStarted?: (traceId: string) => void,
    private readonly onTurnSuperseded?: (oldTraceId: string, newTraceId: string) => void,
    private readonly runtimeOsProbe?: RuntimeOsProbe,
    private readonly activeTurns?: ActiveTurnProjectionPort,
    private readonly onTurnProgress?: (snapshot: NonNullable<ReturnType<ActiveTurnProjectionPort["get"]>>) => void,
    private readonly subagentPort?: SubagentPort,
    private readonly todoPort?: ConversationTodoPort,
    private readonly skillRegistry?: SkillRegistryPort,
    /**
     * Optional hooks that would otherwise force a long positional tail of
     * `undefined`s at every construction site. Prefer adding new hooks here.
     */
    private readonly hooks?: {
      readonly onTurnInterrupted?: (
        partial: AgentTurnPartial,
        context: {
          readonly conversationId: string;
          readonly resume?: boolean;
          readonly interruptReason: "cancel" | "provider" | "max_rounds";
        },
      ) => Promise<void> | void;
    },
  ) {}

  async handle(command: RunAgentTurnCommand): Promise<AgentTurnResult> {
    const providerId = command.providerId ?? this.defaultProviderId;
    const preferredProvider = this.providers.get(providerId);
    if (!preferredProvider) {
      throw new ApplicationError("AGENT_PROVIDER_NOT_FOUND", `AI provider is not configured: ${providerId}`, { providerId });
    }
    const provider = new RoutedAgentProvider({
      providers: this.providers.list(),
      preferredProviderId: preferredProvider.id,
      strategy: this.runtime.strategy,
      totalAttemptBudget: this.runtime.totalAttemptBudget,
      roundRobinCursor: this.roundRobinCursor,
      ...(this.logger ? { logger: this.logger } : {}),
    });
    const compactPrompt = await this.loadCompactPrompt();
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: this.toolGateway,
      defaultMaxToolRounds: this.runtime.maxToolRounds,
      defaultMaxRepeatedToolCalls: this.runtime.maxRepeatedToolCalls,
      softRecoverAttempts: this.runtime.softRecoverAttempts,
      maxConcurrentToolCalls: this.runtime.maxConcurrentToolCalls,
      ...(this.logger ? { logger: this.logger } : {}),
      ...(this.runtime.context ? { context: this.runtime.context } : {}),
      ...(compactPrompt ? { compactPrompt } : {}),
    });
    const worker: AgentTurnWorker = new InProcessAgentTurnWorker((input) => runner.run(input));
    const traceId = command.traceId ?? randomUUID();
    const conversationId = command.conversationId;
    if (command.supersedeTraceId && command.supersedeTraceId !== traceId) {
      this.supersededTraceIds.add(command.supersedeTraceId);
      this.coordinator.cancel(command.supersedeTraceId);
      this.onTurnSuperseded?.(command.supersedeTraceId, traceId);
    }
    this.onTurnStarted?.(traceId);
    if (conversationId && this.activeTurns) {
      this.activeTurns.start({ conversationId, traceId });
      this.publishProgress(conversationId);
    }
    this.toolGateway.beginTurn?.(traceId, {
      ...(command.interactive !== undefined ? { interactive: command.interactive } : {}),
      ...(command.workspace ? { workspace: command.workspace } : {}),
      ...(conversationId ? { conversationId } : {}),
    });
    const injected = command.resume
      ? { messages: command.messages }
      : await this.injectSystemPrompts(command, traceId);
    const messages = injected.messages;
    const promptCache = "promptCache" in injected ? injected.promptCache : undefined;
    let turnEndReason: "completed" | "cancelled" | "failed" | "superseded" = "completed";
    try {
      const result = await this.coordinator.run(traceId, (signal) => worker.run({
        messages,
        pluginIds: command.pluginIds,
        traceId,
        signal,
        ...(command.interactive !== undefined ? { interactive: command.interactive } : {}),
        ...(command.workspace !== undefined ? { workspace: command.workspace } : {}),
        ...(promptCache ? { promptCache } : {}),
        ...(this.onTextDelta || (conversationId && this.activeTurns)
          ? {
              onTextDelta: (delta: string) => {
                this.onTextDelta?.(traceId, delta);
                if (conversationId) this.appendStreaming(conversationId, "text", delta);
              },
            }
          : {}),
        ...(this.onReasoningDelta || (conversationId && this.activeTurns)
          ? {
              onReasoningDelta: (delta: string) => {
                this.onReasoningDelta?.(traceId, delta);
                if (conversationId) this.appendStreaming(conversationId, "reasoning", delta);
              },
            }
          : {}),
        ...(this.onToolCallStart || (conversationId && this.activeTurns)
          ? {
              onToolCallStart: (call: AgentToolCall) => {
                this.onToolCallStart?.(traceId, call);
                if (conversationId && this.activeTurns) {
                  this.streamingBuffers.delete(conversationId);
                  this.activeTurns.openTool(conversationId, call);
                  if (call.name === "ask_question") this.activeTurns.markAwaitingInput(conversationId);
                  this.publishProgress(conversationId);
                }
              },
            }
          : {}),
        ...(this.onToolCallEnd || (conversationId && this.activeTurns)
          ? {
              onToolCallEnd: (execution: AgentToolExecution) => {
                this.onToolCallEnd?.(traceId, execution);
                if (conversationId && this.activeTurns) {
                  this.activeTurns.endTool(conversationId, execution);
                  if (execution.name === "ask_question") this.activeTurns.markRunning(conversationId);
                  this.publishProgress(conversationId);
                }
              },
            }
          : {}),
        ...(this.onContextUpdate ? { onContextUpdate: (update: AgentContextUpdate) => this.onContextUpdate?.(traceId, update) } : {}),
        ...(conversationId && this.activeTurns
          ? {
              onStepsChanged: (steps: readonly AgentTurnStep[]) => {
                this.streamingBuffers.delete(conversationId);
                this.activeTurns!.setSteps(conversationId, steps);
                this.publishProgress(conversationId);
              },
            }
          : {}),
        ...(command.maxToolRounds !== undefined ? { maxToolRounds: command.maxToolRounds } : {}),
        ...(command.model !== undefined ? { model: command.model } : {}),
        ...(command.effort !== undefined ? { effort: command.effort } : {}),
        ...(command.modelCapabilities !== undefined ? { modelCapabilities: command.modelCapabilities } : {}),
      }));
      if (this.onTurnComplete) {
        try {
          await this.onTurnComplete(result, {
            ...(command.conversationId ? { conversationId: command.conversationId } : {}),
            ...(command.resume ? { resume: true } : {}),
          });
        } catch (error) {
          this.logger?.error("onTurnComplete callback failed: %s", error instanceof Error ? error.message : String(error));
        }
      }
      return this.withAutoContinue(result, command);
    } catch (error) {
      if (this.supersededTraceIds.delete(traceId)) {
        turnEndReason = "superseded";
      } else {
        turnEndReason = error instanceof ApplicationError && error.code === "AGENT_TURN_CANCELLED" ? "cancelled" : "failed";
      }
      throw await this.rethrowAfterInterruptSeal(error, conversationId, command.resume === true);
    } finally {
      if (conversationId) {
        this.streamingBuffers.delete(conversationId);
        this.activeTurns?.clear(conversationId, traceId);
      }
      this.onTurnEnd?.(traceId, turnEndReason);
    }
  }

  /**
   * When the runner attached `details.partial`, durable-seal first (main store),
   * then rethrow with slim wire-friendly partial (`messages: []`) so Electron IPC
   * cannot drop the whole error over a multi-MB tool graph.
   */
  private async rethrowAfterInterruptSeal(
    error: unknown,
    conversationId: string | undefined,
    resume: boolean,
  ): Promise<never> {
    const partial = extractTurnPartial(error);
    const onTurnInterrupted = this.hooks?.onTurnInterrupted;
    if (!partial || !conversationId || !onTurnInterrupted) {
      throw error;
    }
    const interruptReason =
      error instanceof ApplicationError && error.code === "AGENT_TURN_CANCELLED"
        ? "cancel"
        : error instanceof ApplicationError && error.code === "AGENT_MAX_TOOL_ROUNDS"
          ? "max_rounds"
          : "provider";
    try {
      await onTurnInterrupted(partial, { conversationId, resume, interruptReason });
    } catch (sealError) {
      this.logger?.error(
        "onTurnInterrupted callback failed: %s",
        sealError instanceof Error ? sealError.message : String(sealError),
      );
      throw error;
    }
    if (error instanceof ApplicationError) {
      const baseDetails = error.details ? { ...error.details } : {};
      const slimPartial: AgentTurnPartial = {
        ...partial,
        messages: [],
      };
      throw new ApplicationError(error.code, error.message, {
        ...baseDetails,
        partial: slimPartial,
        sealedInterrupted: true,
      });
    }
    throw error;
  }

  private appendStreaming(conversationId: string, kind: "text" | "reasoning", delta: string): void {
    if (!this.activeTurns) return;
    const prev = this.streamingBuffers.get(conversationId);
    const next = !prev || prev.kind !== kind
      ? { kind, content: delta }
      : { kind, content: prev.content + delta };
    this.streamingBuffers.set(conversationId, next);
    this.activeTurns.setStreaming(conversationId, next);
    this.publishProgress(conversationId);
  }

  private publishProgress(conversationId: string): void {
    if (!this.onTurnProgress || !this.activeTurns) return;
    const snap = this.activeTurns.get(conversationId);
    if (snap) this.onTurnProgress(snap);
  }

  private async injectSystemPrompts(command: RunAgentTurnCommand, traceId: string) {
    if (!this.promptLoader) return { messages: command.messages };
    try {
      const prompts = await this.promptLoader.loadPrompts();
      const tools = await this.toolGateway.listTools(command.pluginIds, traceId);
      const hasSubagentTool = tools.some((tool) => tool.name === "subagent");
      let subagentRouting: { availableSubagents: string; defaultSubagent: string } | null = null;
      if (hasSubagentTool && this.subagentPort) {
        try {
          subagentRouting = await this.subagentPort.getRoutingInfo();
        } catch (error) {
          this.logger?.warn("Subagent routing info resolve failed: %s", error instanceof Error ? error.message : String(error));
        }
      }
      const vars: PromptVars = {
        currentDate: this.promptSessionDate,
        environment: process.env.NODE_ENV === "production" ? "production" : "development",
        runtimeOs: detectRuntimeOs(this.runtimeOsProbe),
        availableTools: tools.map((tool) => tool.name).join(", "),
        ...(command.workspace ? { workspace: command.workspace } : {}),
        ...(subagentRouting?.availableSubagents ? { availableSubagents: subagentRouting.availableSubagents } : {}),
        ...(subagentRouting?.defaultSubagent ? { defaultSubagent: subagentRouting.defaultSubagent } : {}),
      };
      let memoryPrompt: string | undefined;
      if (this.memoryStore) {
        try {
          const snapshot = await this.memoryStore.loadSnapshot();
          memoryPrompt = formatMemoryPrompt(snapshot);
        } catch (error) {
          this.logger?.warn("Memory snapshot load failed: %s", error instanceof Error ? error.message : String(error));
        }
      }
      let skillsCatalogPrompt: string | undefined;
      if (this.skillRegistry) {
        try {
          const summaries = await this.skillRegistry.list();
          skillsCatalogPrompt = buildSkillsCatalogPrompt(summaries);
        } catch (error) {
          this.logger?.warn("Skills catalog build failed: %s", error instanceof Error ? error.message : String(error));
        }
      }
      const subagentPrompt = hasSubagentTool ? await this.promptLoader.loadSubagentPrompt() : undefined;
      const todoPrompt = this.todoPort && command.conversationId
        ? formatTodoPrompt(this.todoPort.get(command.conversationId))
        : undefined;
      const continuePrompt = (command.autoContinueIndex ?? 0) > 0
        ? await this.loadContinuePrompt()
        : undefined;
      // Live MCP runtime snapshot: full running catalog (name + description +
      // inputSchema) for every tool on running plugins. Built once per
      // agent.run (pre-tool). Duck-typed so stub/review gateways skip it.
      let mcpLivePrompt: string | undefined;
      if (typeof this.toolGateway.getMcpLiveSnapshot === "function") {
        try {
          const snapshot = await this.toolGateway.getMcpLiveSnapshot(traceId);
          mcpLivePrompt = formatMcpLivePrompt(snapshot);
        } catch (error) {
          this.logger?.warn("MCP live snapshot build failed: %s", error instanceof Error ? error.message : String(error));
        }
      }
      const { messages: injected, summary, promptCache } = injectPrompts(prompts, vars, command.messages, command.userPrompt ?? this.userPrompt, memoryPrompt, subagentPrompt, todoPrompt, skillsCatalogPrompt, continuePrompt, mcpLivePrompt);
      this.logger?.debug(summary.toDebugLine(traceId));
      return { messages: injected, promptCache };
    } catch (error) {
      this.logger?.warn("Prompt injection failed, sending raw messages: %s", error instanceof Error ? error.message : String(error));
      return { messages: command.messages };
    }
  }

  private async loadCompactPrompt(): Promise<string | undefined> {
    if (!this.promptLoader) return undefined;
    try {
      return await this.promptLoader.loadCompactPrompt();
    } catch {
      return undefined;
    }
  }

  private async loadContinuePrompt(): Promise<string | undefined> {
    if (!this.promptLoader) return undefined;
    try {
      return await this.promptLoader.loadContinuePrompt();
    } catch {
      return undefined;
    }
  }

  /**
   * Attach the outer multi-turn auto-continue decision to a successful turn
   * result. Only computed when a conversation is bound and a todo port is
   * configured; failed/cancelled paths omit the field entirely.
   */
  private withAutoContinue(result: AgentTurnResult, command: RunAgentTurnCommand): AgentTurnResult {
    if (!command.conversationId || !this.todoPort) return result;
    const decision = decideAutoContinue({
      items: this.todoPort.get(command.conversationId),
      autoContinueIndex: command.autoContinueIndex ?? 0,
      maxAutoContinues: normalizeMaxAutoContinues(this.runtime.maxAutoContinues),
      turnOk: true,
      hasConversation: true,
    });
    return { ...result, autoContinue: decision };
  }
}

function extractTurnPartial(error: unknown): AgentTurnPartial | undefined {
  if (!(error instanceof ApplicationError) || !error.details || typeof error.details !== "object") {
    return undefined;
  }
  const partial = error.details.partial;
  if (!partial || typeof partial !== "object") return undefined;
  const candidate = partial as Partial<AgentTurnPartial>;
  if (typeof candidate.traceId !== "string" || !Array.isArray(candidate.messages)) {
    return undefined;
  }
  if (!Array.isArray(candidate.toolCalls) || !Array.isArray(candidate.steps)) {
    return undefined;
  }
  if (typeof candidate.rounds !== "number" || typeof candidate.text !== "string") {
    return undefined;
  }
  return candidate as AgentTurnPartial;
}
