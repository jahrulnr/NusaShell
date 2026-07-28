import type { NusaClient } from "../client/nusa-client.js";

export type AgentMessage =
  | { readonly role: "system"; readonly content: string }
  | {
      readonly role: "user";
      readonly content: string | readonly (
        | { readonly type: "text"; readonly text: string }
        | { readonly type: "image"; readonly dataUrl: string; readonly name?: string; readonly detail?: "auto" | "low" | "high" }
        | { readonly type: "file"; readonly dataUrl: string; readonly mediaType: string; readonly name: string }
      )[];
    }
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
  readonly providerId?: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly reasoning?: string;
  readonly usage?: {
    readonly inputTokens: number;
    readonly outputTokens: number;
    readonly cachedInputTokens: number;
    readonly cacheWriteTokens: number;
    readonly reasoningOutputTokens: number;
  };
  readonly compaction?: {
    readonly summary: string;
    readonly compactedMessageCount: number;
    readonly estimatedInputTokens: number;
    readonly via: "provider" | "extractive";
  };
}

export class AgentApi {
  constructor(private readonly client: NusaClient) {}

  run(
    messages: readonly AgentMessage[],
    options: {
      readonly pluginIds?: readonly string[];
      readonly providerId?: string;
      readonly model?: string;
      readonly effort?: "auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
      readonly modelCapabilities?: {
        readonly contextWindow?: number;
        readonly maxOutput?: number;
        readonly inputModes?: readonly string[];
        readonly outputModes?: readonly string[];
        readonly supportedEfforts?: readonly ("auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max")[];
        readonly defaultEffort?: "auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
        readonly reasoningSupported?: boolean;
        readonly reasoningMandatory?: boolean;
        readonly reasoningSupportsMaxTokens?: boolean;
        readonly supportsTools?: boolean;
      };
      readonly traceId?: string;
      readonly maxToolRounds?: number;
      readonly timeoutMs?: number;
    } = {},
  ): Promise<AgentTurnResult> {
    return this.client.request("agent.run", {
      messages,
      pluginIds: options.pluginIds ?? [],
      ...(options.providerId !== undefined ? { providerId: options.providerId } : {}),
      ...(options.model !== undefined ? { model: options.model } : {}),
      ...(options.effort !== undefined ? { effort: options.effort } : {}),
      ...(options.modelCapabilities !== undefined ? { modelCapabilities: options.modelCapabilities } : {}),
      ...(options.traceId !== undefined ? { traceId: options.traceId } : {}),
      ...(options.maxToolRounds !== undefined ? { maxToolRounds: options.maxToolRounds } : {}),
    }, options.timeoutMs);
  }

  cancel(traceId: string, timeoutMs?: number): Promise<{ readonly traceId: string; readonly cancelled: boolean }> {
    return this.client.request("agent.cancel", { traceId }, timeoutMs);
  }
}
