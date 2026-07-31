import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AgentTurnCoordinator } from "../../services/agent-turn-coordinator.js";
import type { CancelAgentTurnCommand } from "./cancel-agent-turn.command.js";

export interface CancelAgentTurnResult {
  readonly traceId: string;
  readonly cancelled: boolean;
  readonly phase: "requested";
}

export class CancelAgentTurnHandler implements CommandHandler<CancelAgentTurnCommand, CancelAgentTurnResult> {
  constructor(
    private readonly coordinator: AgentTurnCoordinator,
    private readonly onCancelRequested?: (traceId: string) => void,
  ) {}

  async handle(command: CancelAgentTurnCommand): Promise<CancelAgentTurnResult> {
    const cancelled = this.coordinator.cancel(command.traceId);
    if (cancelled) this.onCancelRequested?.(command.traceId);
    return { traceId: command.traceId, cancelled, phase: "requested" };
  }
}
