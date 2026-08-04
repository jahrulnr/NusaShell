import type { AgentProviderRequest, AgentProviderResult } from "@nusashell/application";
import { ChatApiStrategy } from "./openai-chat-strategy.js";
import { MessagesApiStrategy } from "./openai-messages-strategy.js";
import { ResponsesApiStrategy } from "./openai-responses-strategy.js";

export type ProviderApi = "chat" | "responses" | "messages";

/**
 * Strategy for a specific OpenAI-compatible API mode (chat, responses, messages).
 * Each strategy knows how to build the request body and parse the response for its mode.
 */
export interface ApiStrategy {
  readonly api: ProviderApi;
  readonly endpointPath: string;
  readonly supportsStream: boolean;
  readonly sseMode: "chat" | "responses" | "messages";
  buildBody(
    request: AgentProviderRequest,
    model: string,
    allowVision: boolean,
    maxOutputTokens: number | undefined,
  ): Record<string, unknown>;
  parseResult(payload: unknown, fallbackModel: string): AgentProviderResult;
}

export function createApiStrategy(api: ProviderApi): ApiStrategy {
  if (api === "responses") return new ResponsesApiStrategy();
  if (api === "messages") return new MessagesApiStrategy();
  return new ChatApiStrategy();
}

// --- Strategy implementations are in separate modules and re-exported ---
export { ChatApiStrategy, MessagesApiStrategy, ResponsesApiStrategy };
