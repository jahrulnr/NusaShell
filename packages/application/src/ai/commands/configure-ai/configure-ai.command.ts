import type { Command } from "../../../messaging/command.js";

export interface ConfigureAiCommand extends Command {
  readonly kind: "configure-ai";
  readonly providerId: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly model?: string;
  readonly baseUrl?: string;
  readonly apiKey?: string;
  readonly timeoutMs?: number;
  readonly maxAttempts?: number;
  readonly omitToolChoice?: boolean;
}
