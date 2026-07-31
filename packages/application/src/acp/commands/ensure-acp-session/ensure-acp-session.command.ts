import type { Command } from "../../../messaging/command.js";
import type { AcpSessionInfo } from "../../services/acp-session-service.js";

export interface EnsureAcpSessionCommand extends Command {
  readonly kind: "ensure-acp-session";
  readonly conversationId: string;
  readonly workspace: string | undefined;
  readonly provider: {
    readonly providerId: string;
    readonly command: string;
    readonly args: readonly string[];
    readonly authMethodId?: string;
  };
}

export type EnsureAcpSessionResult = AcpSessionInfo | null;
