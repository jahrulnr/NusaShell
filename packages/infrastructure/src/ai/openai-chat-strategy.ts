import type { AgentContentPart, AgentProviderRequest, AgentProviderResult } from "@nusashell/application";
import { mergeTextToolCalls } from "./text-tool-call-parser.js";
import type { ApiStrategy } from "./openai-api-strategy.js";
import {
  emptySchema,
  extractContentText,
  firstText,
  malformedResponseError,
  mapMessages,
  parseToolCall,
  parseUsage,
  positiveInteger,
  requireRecord,
  validDataUrl,
} from "./openai-shared.js";

export class ChatApiStrategy implements ApiStrategy {
  readonly api = "chat" as const;
  readonly endpointPath = "chat/completions";
  readonly supportsStream = true;
  readonly sseMode = "chat" as const;

  buildBody(
    request: AgentProviderRequest,
    model: string,
    allowVision: boolean,
    maxOutputTokens: number | undefined,
  ): Record<string, unknown> {
    const tools = request.tools.map(toChatTool);
    return {
      model,
      messages: mapMessages(request.messages, (parts) => toChatContent(parts, allowVision)),
      ...(tools.length > 0 ? { tools, tool_choice: "auto" } : {}),
      ...(positiveInteger(maxOutputTokens) ? { max_tokens: maxOutputTokens } : {}),
      ...chatEffort(request.effort),
    };
  }

  parseResult(payload: unknown, fallbackModel: string): AgentProviderResult {
    const root = requireRecord(payload, "Provider response is not an object");
    const choices = Array.isArray(root.choices) ? root.choices : [];
    const choice = choices[0];
    if (!choice || typeof choice !== "object") {
      throw malformedResponseError("Provider response does not contain a completion choice", payload);
    }
    const message = requireRecord((choice as Record<string, unknown>).message, "Provider response does not contain a completion message");
    const nativeCalls = Array.isArray(message.tool_calls) ? message.tool_calls.map(parseToolCall) : [];
    const merged = mergeTextToolCalls(nativeCalls, extractContentText(message.content));
    const reasoning = firstText(message.reasoning_content, message.reasoning, message.thinking, message.reasoning_text, message.thinking_content);
    const usage = parseUsage(root.usage);
    return {
      ...(merged.text ? { text: merged.text } : {}),
      ...(merged.calls.length > 0 ? { toolCalls: merged.calls } : {}),
      ...(reasoning ? { reasoning } : {}),
      model: typeof root.model === "string" ? root.model : fallbackModel,
      ...(typeof choice.finish_reason === "string" ? { status: choice.finish_reason } : {}),
      ...(usage ? { usage } : {}),
    };
  }
}

function toChatTool(tool: AgentProviderRequest["tools"][number]): Record<string, unknown> {
  return {
    type: "function",
    function: {
      name: tool.name,
      ...(tool.description ? { description: tool.description } : {}),
      parameters: tool.inputSchema ?? emptySchema(),
    },
  };
}

function toChatContent(parts: readonly AgentContentPart[], allowVision: boolean): unknown {
  const output: Record<string, unknown>[] = [];
  for (const part of parts) {
    if (part.type === "text") output.push({ type: "text", text: part.text });
    if (part.type === "image" && allowVision && validDataUrl(part.dataUrl, "image/")) {
      output.push({
        type: "image_url",
        image_url: { url: part.dataUrl, ...(part.detail ? { detail: part.detail } : {}) },
      });
    }
    if (part.type === "file") {
      output.push({ type: "text", text: `[Attached document: ${part.name} (${part.mediaType})]` });
    }
  }
  return output.length > 0 ? output : "";
}

function chatEffort(effort: AgentProviderRequest["effort"]): Record<string, unknown> {
  if (!effort || effort === "auto") return {};
  if (effort === "none") {
    return {
      thinking: { type: "disabled" },
      reasoning_effort: effort,
      reasoning: { effort },
    };
  }
  return {
    thinking: { type: "enabled" },
    reasoning_effort: effort,
    reasoning: { effort },
  };
}
