import type { Command } from "../../../messaging/command.js";
import type {
  AgentMessage,
  AgentModelCapabilities,
  ReasoningEffort,
} from "../../ports/agent-provider.port.js";

export interface RunAgentTurnCommand extends Command {
  readonly kind: "run-agent-turn";
  readonly providerId?: string;
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
  readonly modelCapabilities?: AgentModelCapabilities;
  readonly userPrompt?: string;
  readonly traceId?: string;
  readonly workspace?: string;
  readonly interactive?: boolean;
  readonly resume?: boolean;
  readonly supersedeTraceId?: string;
}
