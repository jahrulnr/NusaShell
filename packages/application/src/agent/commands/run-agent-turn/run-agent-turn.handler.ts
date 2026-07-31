import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentProviderRegistryPort, AgentToolCall } from "../../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../../ports/agent-tool-gateway.port.js";
import type { PromptLoaderPort } from "../../ports/prompt-loader.port.js";
import {
  AgentTurnRunner,
  type AgentContextOptions,
  type AgentTurnResult,
  type AgentToolExecution,
  type AgentContextUpdate,
} from "../../services/agent-turn-runner.js";
import { injectPrompts, type PromptVars } from "../../services/prompt-injector.js";
import { formatMemoryPrompt } from "../../services/memory-prompt-formatter.js";
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
  strategy: AgentProviderStrategy;
  totalAttemptBudget: number;
  context?: AgentContextOptions;
}

export class RunAgentTurnHandler implements CommandHandler<RunAgentTurnCommand, AgentTurnResult> {
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
      ...(this.logger ? { logger: this.logger } : {}),
    });
    const compactPrompt = await this.loadCompactPrompt();
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: this.toolGateway,
      defaultMaxToolRounds: this.runtime.maxToolRounds,
      defaultMaxRepeatedToolCalls: this.runtime.maxRepeatedToolCalls,
      ...(this.logger ? { logger: this.logger } : {}),
      ...(this.runtime.context ? { context: this.runtime.context } : {}),
      ...(compactPrompt ? { compactPrompt } : {}),
    });
    const worker: AgentTurnWorker = new InProcessAgentTurnWorker((input) => runner.run(input));
    const traceId = command.traceId ?? randomUUID();
    const messages = await this.injectSystemPrompts(command, traceId);
    return this.coordinator.run(traceId, (signal) => worker.run({
      messages,
      pluginIds: command.pluginIds,
      traceId,
      signal,
      ...(this.onTextDelta ? { onTextDelta: (delta) => this.onTextDelta?.(traceId, delta) } : {}),
      ...(this.onReasoningDelta ? { onReasoningDelta: (delta) => this.onReasoningDelta?.(traceId, delta) } : {}),
      ...(this.onToolCallStart ? { onToolCallStart: (call) => this.onToolCallStart?.(traceId, call) } : {}),
      ...(this.onToolCallEnd ? { onToolCallEnd: (execution) => this.onToolCallEnd?.(traceId, execution) } : {}),
      ...(this.onContextUpdate ? { onContextUpdate: (update) => this.onContextUpdate?.(traceId, update) } : {}),
      ...(command.maxToolRounds !== undefined ? { maxToolRounds: command.maxToolRounds } : {}),
      ...(command.model !== undefined ? { model: command.model } : {}),
      ...(command.effort !== undefined ? { effort: command.effort } : {}),
      ...(command.modelCapabilities !== undefined ? { modelCapabilities: command.modelCapabilities } : {}),
    }));
  }

  private async injectSystemPrompts(command: RunAgentTurnCommand, traceId: string) {
    if (!this.promptLoader) return command.messages;
    try {
      const prompts = await this.promptLoader.loadPrompts();
      const tools = await this.toolGateway.listTools(command.pluginIds, traceId);
      const vars: PromptVars = {
        currentDate: new Date().toISOString().slice(0, 10),
        environment: process.env.NODE_ENV === "production" ? "production" : "development",
        availableTools: tools.map((tool) => tool.name).join(", "),
        ...(command.workspace ? { workspace: command.workspace } : {}),
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
      return injectPrompts(prompts, vars, command.messages, command.userPrompt ?? this.userPrompt, memoryPrompt);
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
