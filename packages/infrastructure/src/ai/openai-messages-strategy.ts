import type { AgentContentPart, AgentProviderRequest, AgentProviderResult, AgentToolCall } from "@nusashell/application";
import { mergeTextToolCalls } from "./text-tool-call-parser.js";
import type { ApiStrategy } from "./openai-api-strategy.js";
import {
  emptySchema,
  firstText,
  limitAttachments,
  parseDataUrl,
  parseUsage,
  positiveInteger,
  record,
  requireRecord,
  MAX_IMAGES_PER_MESSAGE,
} from "./openai-shared.js";

export class MessagesApiStrategy implements ApiStrategy {
  readonly api = "messages" as const;
  readonly endpointPath = "messages";
  readonly supportsStream = true;
  readonly sseMode = "messages" as const;

  buildBody(
    request: AgentProviderRequest,
    model: string,
    allowVision: boolean,
    maxOutputTokens: number | undefined,
  ): Record<string, unknown> {
    const system: string[] = [];
    const messages: Record<string, unknown>[] = [];
    for (const message of request.messages) {
      if (message.role === "system") {
        system.push(message.content);
      } else if (message.role === "tool") {
        messages.push({ role: "user", content: [{ type: "tool_result", tool_use_id: message.toolCallId, content: message.content }] });
      } else if (message.role === "assistant") {
        const content: Record<string, unknown>[] = [];
        if (message.content) content.push({ type: "text", text: message.content });
        for (const call of message.toolCalls ?? []) content.push({ type: "tool_use", id: call.id, name: call.name, input: call.args });
        if (content.length > 0) messages.push({ role: "assistant", content });
      } else {
        messages.push({
          role: "user",
          content: typeof message.content === "string"
            ? message.content
            : toMessagesContent(message.content, allowVision),
        });
      }
    }
    return {
      model,
      messages,
      max_tokens: positiveInteger(maxOutputTokens) ? maxOutputTokens : 8192,
      ...(system.length > 0 ? { system: system.join("\n\n") } : {}),
      ...(request.tools.length > 0 ? {
        tools: request.tools.map((tool) => ({
          name: tool.name,
          ...(tool.description ? { description: tool.description } : {}),
          input_schema: tool.inputSchema ?? emptySchema(),
        })),
      } : {}),
    };
  }

  parseResult(payload: unknown, fallbackModel: string): AgentProviderResult {
    const root = requireRecord(payload, "Provider response is not an object");
    if (!Array.isArray(root.content)) throw new Error("Provider response does not contain Messages API content");
    const text: string[] = [];
    const reasoning: string[] = [];
    const nativeCalls: AgentToolCall[] = [];
    for (const raw of root.content) {
      const item = record(raw);
      if (item.type === "text" && typeof item.text === "string") text.push(item.text);
      if (item.type === "thinking" || item.type === "reasoning") {
        const thought = firstText(item.thinking, item.text, item.reasoning);
        if (thought) reasoning.push(thought);
      }
      if (item.type === "tool_use" && typeof item.name === "string") {
        nativeCalls.push({
          id: typeof item.id === "string" ? item.id : `call_message_${nativeCalls.length + 1}`,
          name: item.name,
          args: record(item.input),
        });
      }
    }
    const merged = mergeTextToolCalls(nativeCalls, text.join(""));
    const usage = parseUsage(root.usage);
    return {
      ...(merged.text ? { text: merged.text } : {}),
      ...(merged.calls.length > 0 ? { toolCalls: merged.calls } : {}),
      ...(reasoning.length > 0 ? { reasoning: reasoning.join("\n\n") } : {}),
      model: typeof root.model === "string" ? root.model : fallbackModel,
      ...(typeof root.stop_reason === "string" ? { status: root.stop_reason } : {}),
      ...(usage ? { usage } : {}),
    };
  }
}

function toMessagesContent(parts: readonly AgentContentPart[], allowVision: boolean): readonly Record<string, unknown>[] {
  const output: Record<string, unknown>[] = [];
  for (const part of limitAttachments(parts, MAX_IMAGES_PER_MESSAGE)) {
    if (part.type === "text") output.push({ type: "text", text: part.text });
    if (part.type === "image" && allowVision) {
      const data = parseDataUrl(part.dataUrl);
      if (data?.mediaType.startsWith("image/")) {
        output.push({ type: "image", source: { type: "base64", media_type: data.mediaType, data: data.base64 } });
      }
    }
    if (part.type === "file") {
      const data = parseDataUrl(part.dataUrl);
      if (data) output.push({ type: "document", source: { type: "base64", media_type: data.mediaType, data: data.base64 } });
    }
  }
  return output.length > 0 ? output : [{ type: "text", text: "" }];
}
