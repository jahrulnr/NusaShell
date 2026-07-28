import type { NusaClient } from "../client/nusa-client.js";

export type AgentMessage =
  | { readonly role: "system" | "user"; readonly content: string }
  | {
      readonly role: "assistant";
      readonly content?: string;
      readonly toolCalls?: readonly { readonly id: string; readonly name: string; readonly args: Readonly<Record<string, unknown>> }[];
    }
  | { readonly role: "tool"; readonly toolCallId: string; readonly name: string; readonly content: string };

export interface AgentTurnResult {
  readonly traceId: string;
  readonly text: string;
  readonly rounds: number;
  readonly toolCalls: readonly unknown[];
  readonly model?: string;
}

export class AgentApi {
  constructor(private readonly client: NusaClient) {}

  run(
    messages: readonly AgentMessage[],
    options: {
      readonly pluginIds?: readonly string[];
      readonly providerId?: string;
      readonly maxToolRounds?: number;
      readonly timeoutMs?: number;
    } = {},
  ): Promise<AgentTurnResult> {
    return this.client.request("agent.run", {
      messages,
      pluginIds: options.pluginIds ?? [],
      ...(options.providerId !== undefined ? { providerId: options.providerId } : {}),
      ...(options.maxToolRounds !== undefined ? { maxToolRounds: options.maxToolRounds } : {}),
    }, options.timeoutMs);
  }
}
