import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentTurnCoordinator } from "../../services/agent-turn-coordinator.js";
import type { CancelAgentTurnCommand } from "./cancel-agent-turn.command.js";

export interface CancelAgentTurnResult {
  readonly traceId: string;
  readonly cancelled: boolean;
}

export class CancelAgentTurnHandler implements CommandHandler<CancelAgentTurnCommand, CancelAgentTurnResult> {
  constructor(private readonly coordinator: AgentTurnCoordinator) {}

  async handle(command: CancelAgentTurnCommand): Promise<CancelAgentTurnResult> {
    return { traceId: command.traceId, cancelled: this.coordinator.cancel(command.traceId) };
  }
}
