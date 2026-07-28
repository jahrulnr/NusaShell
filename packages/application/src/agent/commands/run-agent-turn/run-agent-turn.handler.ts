import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentProviderRegistryPort } from "../../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../../ports/agent-tool-gateway.port.js";
import {
  AgentTurnRunner,
  type AgentContextOptions,
  type AgentTurnResult,
} from "../../services/agent-turn-runner.js";
import { InProcessAgentTurnWorker, type AgentTurnWorker } from "../../services/in-process-agent-turn-worker.js";
import type { RunAgentTurnCommand } from "./run-agent-turn.command.js";
import type { LoggerPort } from "../../../plugin/ports/logger.port.js";
import {
  RoutedAgentProvider,
  type AgentProviderStrategy,
} from "../../services/routed-agent-provider.js";
import { AgentTurnCoordinator } from "../../services/agent-turn-coordinator.js";
import { randomUUID } from "node:crypto";

export class RunAgentTurnHandler implements CommandHandler<RunAgentTurnCommand, AgentTurnResult> {
  constructor(
    private readonly providers: AgentProviderRegistryPort,
    private readonly toolGateway: AgentToolGateway,
    private readonly defaultProviderId: string,
    private readonly defaultMaxToolRounds: number,
    private readonly logger?: LoggerPort,
    private readonly context?: AgentContextOptions,
    private readonly routing: {
      readonly strategy: AgentProviderStrategy;
      readonly totalAttemptBudget: number;
    } = { strategy: "failover", totalAttemptBudget: 4 },
    private readonly coordinator: AgentTurnCoordinator = new AgentTurnCoordinator(),
    private readonly onTextDelta?: (traceId: string, delta: string) => void,
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
      strategy: this.routing.strategy,
      totalAttemptBudget: this.routing.totalAttemptBudget,
    });
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: this.toolGateway,
      defaultMaxToolRounds: this.defaultMaxToolRounds,
      ...(this.logger ? { logger: this.logger } : {}),
      ...(this.context ? { context: this.context } : {}),
    });
    const worker: AgentTurnWorker = new InProcessAgentTurnWorker((input) => runner.run(input));
    const traceId = command.traceId ?? randomUUID();
    return this.coordinator.run(traceId, (signal) => worker.run({
      messages: command.messages,
      pluginIds: command.pluginIds,
      traceId,
      signal,
      ...(this.onTextDelta ? { onTextDelta: (delta) => this.onTextDelta?.(traceId, delta) } : {}),
      ...(command.maxToolRounds !== undefined ? { maxToolRounds: command.maxToolRounds } : {}),
      ...(command.model !== undefined ? { model: command.model } : {}),
      ...(command.effort !== undefined ? { effort: command.effort } : {}),
      ...(command.modelCapabilities !== undefined ? { modelCapabilities: command.modelCapabilities } : {}),
    }));
  }
}
