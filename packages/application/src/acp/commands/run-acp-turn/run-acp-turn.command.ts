import type { Command } from "../../../messaging/command.js";
import type { AcpContentBlock, AcpProviderDescriptor } from "../../ports/acp-client.port.js";

export interface RunAcpTurnCommand extends Command {
  readonly kind: "run-acp-turn";
  readonly traceId: string;
  readonly conversationId: string;
  readonly workspace?: string;
  readonly provider: AcpProviderDescriptor;
  readonly prompt: readonly AcpContentBlock[];
}
