import type { Command } from "../../../messaging/command.js";
import type { AcpConfigOption } from "../../ports/acp-client.port.js";

export interface SetAcpConfigOptionCommand extends Command {
  readonly kind: "set-acp-config-option";
  readonly conversationId: string;
  readonly configId: string;
  readonly value: string | boolean;
}

export type SetAcpConfigOptionResult = readonly AcpConfigOption[];
