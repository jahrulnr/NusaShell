import type { Command } from "../../../messaging/command.js";
import type { AgentMessage } from "../../ports/agent-provider.port.js";

export interface RunAgentTurnCommand extends Command {
  readonly kind: "run-agent-turn";
  readonly providerId?: string;
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
}
