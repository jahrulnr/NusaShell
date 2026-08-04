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
  type AgentToolExecution,
  type AgentContextUpdate,
  type AgentTurnStep,
} from "../../services/agent-turn-runner.js";
import { injectPrompts, type PromptVars } from "../../services/prompt-injector.js";
import { detectRuntimeOs, type RuntimeOsProbe } from "../../services/runtime-os.js";
import { formatMemoryPrompt } from "../../services/memory-prompt-formatter.js";
import { formatTodoPrompt } from "../../services/todo-prompt-formatter.js";
import type { ConversationTodoPort } from "../../ports/conversation-todo.port.js";
import type { MemoryStorePort } from "../../../memory/ports/memory-store.port.js";
import { InProcessAgentTurnWorker, type AgentTurnWorker } from "../../services/in-process-agent-turn-worker.js";
import type { RunAgentTurnCommand } from "./run-agent-turn.command.js";
import type { LoggerPort } from "../../../plugin/ports/logger.port.js";
import {
  RoutedAgentProvider,
  type AgentProviderStrategy,
} from "../../services/routed-agent-provider.js";
import { AgentTurnCoordinator } from "../../services/agent-turn-coordinator.js";
import { randomUUID } from "node:crypto";

export interface AgentRuntimeSettings {
  maxToolRounds: number;
  maxRepeatedToolCalls: number;
  softRecoverAttempts: number;
  maxConcurrentToolCalls: number;
  strategy: AgentProviderStrategy;
  totalAttemptBudget: number;
  context?: AgentContextOptions;
}

export class RunAgentTurnHandler implements CommandHandler<RunAgentTurnCommand, AgentTurnResult> {
  private readonly supersededTraceIds = new Set<string>();
  /** Open streaming segment contents per conversation (token-level). */
  private readonly streamingBuffers = new Map<string, { kind: "text" | "reasoning"; content: string }>();
  /** Process-lifetime round-robin cursor shared across all turns (A2). */
  private readonly roundRobinCursor = { value: 0 };

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
    const messages = command.resume
      ? command.messages
      : await this.injectSystemPrompts(command, traceId);
    let turnEndReason: "completed" | "cancelled" | "failed" | "superseded" = "completed";
    try {
      const result = await this.coordinator.run(traceId, (signal) => worker.run({
        messages,
        pluginIds: command.pluginIds,
        traceId,
        signal,
        ...(command.interactive !== undefined ? { interactive: command.interactive } : {}),
        ...(command.workspace !== undefined ? { workspace: command.workspace } : {}),
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
      return result;
    } catch (error) {
      if (this.supersededTraceIds.delete(traceId)) {
        turnEndReason = "superseded";
      } else {
        turnEndReason = error instanceof ApplicationError && error.code === "AGENT_TURN_CANCELLED" ? "cancelled" : "failed";
      }
      throw error;
    } finally {
      if (conversationId) {
        this.streamingBuffers.delete(conversationId);
        this.activeTurns?.clear(conversationId, traceId);
      }
      this.onTurnEnd?.(traceId, turnEndReason);
    }
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
    if (!this.promptLoader) return command.messages;
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
        currentDate: new Date().toISOString().slice(0, 10),
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
      const subagentPrompt = hasSubagentTool ? await this.promptLoader.loadSubagentPrompt() : undefined;
      const todoPrompt = this.todoPort && command.conversationId
        ? formatTodoPrompt(this.todoPort.get(command.conversationId))
        : undefined;
      const { messages: injected, summary } = injectPrompts(prompts, vars, command.messages, command.userPrompt ?? this.userPrompt, memoryPrompt, subagentPrompt, todoPrompt);
      this.logger?.debug(summary.toDebugLine(traceId));
      return injected;
    } catch (error) {
      this.logger?.warn("Prompt injection failed, sending raw messages: %s", error instanceof Error ? error.message : String(error));
      return command.messages;
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
}
