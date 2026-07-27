import type { Command } from "../../../messaging/command.js";

export interface CallToolCommand extends Command {
  readonly kind: "call-tool";
  readonly pluginId: string;
  readonly requestId: string;
  readonly toolName: string;
  readonly args: Readonly<Record<string, unknown>>;
  readonly timeoutMs?: number;
}
