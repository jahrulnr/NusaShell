import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentProviderRegistryPort } from "../../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../../ports/agent-tool-gateway.port.js";
import { AgentTurnRunner, type AgentTurnResult } from "../../services/agent-turn-runner.js";
import { InProcessAgentTurnWorker, type AgentTurnWorker } from "../../services/in-process-agent-turn-worker.js";
import type { RunAgentTurnCommand } from "./run-agent-turn.command.js";
import type { LoggerPort } from "../../../plugin/ports/logger.port.js";

export class RunAgentTurnHandler implements CommandHandler<RunAgentTurnCommand, AgentTurnResult> {
  constructor(
    private readonly providers: AgentProviderRegistryPort,
    private readonly toolGateway: AgentToolGateway,
    private readonly defaultProviderId: string,
    private readonly defaultMaxToolRounds: number,
    private readonly logger?: LoggerPort,
  ) {}

  async handle(command: RunAgentTurnCommand): Promise<AgentTurnResult> {
    const providerId = command.providerId ?? this.defaultProviderId;
    const provider = this.providers.get(providerId);
    if (!provider) {
      throw new ApplicationError("AGENT_PROVIDER_NOT_FOUND", `AI provider is not configured: ${providerId}`, { providerId });
    }
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: this.toolGateway,
      defaultMaxToolRounds: this.defaultMaxToolRounds,
      ...(this.logger ? { logger: this.logger } : {}),
    });
    const worker: AgentTurnWorker = new InProcessAgentTurnWorker((input) => runner.run(input));
    return worker.run({
      messages: command.messages,
      pluginIds: command.pluginIds,
      ...(command.maxToolRounds !== undefined ? { maxToolRounds: command.maxToolRounds } : {}),
    });
  }
}
