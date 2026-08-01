import type { AgentContentPart, AgentProviderRequest, AgentProviderResult, AgentToolCall } from "@nusashell/application";
import { mergeTextToolCalls } from "./text-tool-call-parser.js";
import type { ApiStrategy } from "./openai-api-strategy.js";
import {
  emptySchema,
  extractContentText,
  firstText,
  limitAttachments,
  parseToolArguments,
  parseUsage,
  positiveInteger,
  record,
  requireRecord,
  validDataUrl,
  MAX_IMAGES_PER_MESSAGE,
} from "./openai-shared.js";

export class ResponsesApiStrategy implements ApiStrategy {
  readonly api = "responses" as const;
  readonly endpointPath = "responses";
  readonly supportsStream = true;
  readonly sseMode = "responses" as const;

  buildBody(
    request: AgentProviderRequest,
    model: string,
    allowVision: boolean,
    maxOutputTokens: number | undefined,
  ): Record<string, unknown> {
    const instructions: string[] = [];
    const input: Record<string, unknown>[] = [];
    for (const message of request.messages) {
      if (message.role === "system") {
        instructions.push(message.content);
      } else if (message.role === "tool") {
        input.push({
          type: "function_call_output",
          call_id: message.toolCallId,
          output: message.content,
        });
      } else if (message.role === "assistant") {
        if (message.content) input.push({ role: "assistant", content: message.content });
        for (const call of message.toolCalls ?? []) {
          input.push({ type: "function_call", call_id: call.id, name: call.name, arguments: JSON.stringify(call.args) });
        }
      } else {
        input.push({
          role: "user",
          content: typeof message.content === "string"
            ? message.content
            : toResponsesContent(message.content, allowVision),
        });
      }
    }
    const tools = request.tools.map(toResponsesTool);
    return {
      model,
      input,
      ...(instructions.length > 0 ? { instructions: instructions.join("\n\n") } : {}),
      ...(tools.length > 0 ? { tools, tool_choice: "auto" } : {}),
      ...(positiveInteger(maxOutputTokens) ? { max_output_tokens: maxOutputTokens } : {}),
      ...responsesEffort(request.effort),
    };
  }

  parseResult(payload: unknown, fallbackModel: string): AgentProviderResult {
    const root = requireRecord(payload, "Provider response is not an object");
    if (!Array.isArray(root.output)) throw new Error("Provider response does not contain Responses API output");
    const text: string[] = [];
    const reasoning: string[] = [];
    const nativeCalls: AgentToolCall[] = [];
    for (const raw of root.output) {
      const item = record(raw);
      if (item.type === "function_call" && typeof item.name === "string") {
        nativeCalls.push({
          id: firstText(item.call_id, item.id) || `call_response_${nativeCalls.length + 1}`,
          name: item.name,
          args: parseToolArguments(item.arguments),
        });
      }
      if (item.type === "message") text.push(extractContentText(item.content));
      if (item.type === "reasoning") {
        reasoning.push(extractContentText(item.summary), extractContentText(item.content), firstText(item.text));
      }
    }
    if (text.join("") === "" && typeof root.output_text === "string") text.push(root.output_text);
    const merged = mergeTextToolCalls(nativeCalls, text.join(""));
    const reasoningText = reasoning.filter(Boolean).join("\n\n");
    const usage = parseUsage(root.usage);
    return {
      ...(merged.text ? { text: merged.text } : {}),
      ...(merged.calls.length > 0 ? { toolCalls: merged.calls } : {}),
      ...(reasoningText ? { reasoning: reasoningText } : {}),
      model: typeof root.model === "string" ? root.model : fallbackModel,
      ...(typeof root.status === "string" ? { status: root.status } : {}),
      ...(usage ? { usage } : {}),
    };
  }
}

function toResponsesTool(tool: AgentProviderRequest["tools"][number]): Record<string, unknown> {
  return {
    type: "function",
    name: tool.name,
    ...(tool.description ? { description: tool.description } : {}),
    parameters: tool.inputSchema ?? emptySchema(),
  };
}

function toResponsesContent(parts: readonly AgentContentPart[], allowVision: boolean): readonly Record<string, unknown>[] {
  const output: Record<string, unknown>[] = [];
  for (const part of limitAttachments(parts, MAX_IMAGES_PER_MESSAGE)) {
    if (part.type === "text") output.push({ type: "input_text", text: part.text });
    if (part.type === "image" && allowVision && validDataUrl(part.dataUrl, "image/")) {
      output.push({ type: "input_image", image_url: part.dataUrl, ...(part.detail ? { detail: part.detail } : {}) });
    }
    if (part.type === "file" && validDataUrl(part.dataUrl, part.mediaType)) {
      output.push({ type: "input_file", file_data: part.dataUrl, filename: part.name });
    }
  }
  return output.length > 0 ? output : [{ type: "input_text", text: "" }];
}

function responsesEffort(effort: AgentProviderRequest["effort"]): Record<string, unknown> {
  return effort && effort !== "auto" ? { reasoning: { effort } } : {};
}
