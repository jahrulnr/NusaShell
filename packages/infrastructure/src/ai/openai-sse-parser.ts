import type { AgentToolCall } from "@nusashell/application";

export class SseTransportError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SseTransportError";
  }
}

export async function parseOpenAiSse(
  response: Response,
  api: "chat" | "responses",
  onTextDelta: ((delta: string) => void) | undefined,
  onReasoningDelta: ((delta: string) => void) | undefined,
  maxBytes: number,
): Promise<unknown> {
  if (!response.body) throw new SseTransportError("SSE response has no body");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let bytes = 0;
  let completed = false;
  let finalPayload: unknown;
  const chat = new ChatAccumulator(onTextDelta, onReasoningDelta);
  const responses = new ResponsesAccumulator(onTextDelta, onReasoningDelta);
  const acceptBlock = (block: string) => {
    const lines = block.split(/\r?\n/);
    const data = lines
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trimStart())
      .join("\n");
    if (!data) return;
    if (data === "[DONE]") {
      completed = true;
      return;
    }
    const eventType = lines
      .find((line) => line.startsWith("event:"))
      ?.slice(6)
      .trim();
    let event: unknown;
    try {
      event = JSON.parse(data);
    } catch {
      return;
    }
    if (api === "chat") {
      chat.accept(event, eventType);
      if (chat.completed) completed = true;
    } else {
      const accepted = responses.accept(event);
      if (accepted.completed) completed = true;
      if (accepted.finalPayload !== undefined) finalPayload = accepted.finalPayload;
    }
  };

  while (true) {
    let chunk;
    try {
      chunk = await reader.read();
    } catch (error) {
      throw new SseTransportError(`SSE read failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (chunk.done) break;
    bytes += chunk.value.byteLength;
    if (bytes > maxBytes) throw new SseTransportError("SSE response exceeded the configured size limit");
    buffer += decoder.decode(chunk.value, { stream: true });
    const blocks = buffer.split(/\r?\n\r?\n/);
    buffer = blocks.pop() ?? "";
    for (const block of blocks) acceptBlock(block);
  }
  if (buffer.trim()) acceptBlock(buffer);

  if (!completed) throw new SseTransportError("SSE stream ended before completion");
  return finalPayload ?? (api === "chat" ? chat.payload() : responses.payload());
}

class ChatAccumulator {
  private text = "";
  private reasoning = "";
  private model = "";
  private finishReason = "";
  private usage: unknown;
  private readonly tools = new Map<number, { id: string; name: string; arguments: string }>();
  completed = false;

  constructor(
    private readonly onTextDelta?: (delta: string) => void,
    private readonly onReasoningDelta?: (delta: string) => void,
  ) {}

  accept(value: unknown, eventType?: string): void {
    const event = record(value);
    if (typeof event.model === "string") this.model = event.model;
    if (event.usage !== undefined) this.usage = event.usage;

    // Some providers send reasoning as a separate SSE event type (e.g.
    // `event: reasoning\ndata: {"delta": "..."}`) with the reasoning text at
    // the top level, not inside choices[0].delta.
    if (eventType === "reasoning" || eventType === "thinking" || eventType === "reasoning_delta") {
      const reasoningDelta = reasoningTextValue(event.delta) || reasoningTextValue(event.text) || reasoningTextValue(event.reasoning);
      if (reasoningDelta) {
        this.reasoning += reasoningDelta;
        this.onReasoningDelta?.(reasoningDelta);
      }
    }

    const choices = Array.isArray(event.choices) ? event.choices : [];
    for (const rawChoice of choices) {
      const choice = record(rawChoice);
      const delta = record(choice.delta);
      const text = textValue(delta.content);
      if (text) {
        this.text += text;
        this.onTextDelta?.(text);
      }
      // Check all known reasoning field names. Some providers use
      // reasoning_text, thinking_content, or reasoning_details instead of
      // the more common reasoning_content / reasoning / thinking.
      const reasoningDelta =
        reasoningTextValue(delta.reasoning_content) ||
        reasoningTextValue(delta.reasoning) ||
        reasoningTextValue(delta.thinking) ||
        reasoningTextValue(delta.reasoning_text) ||
        reasoningTextValue(delta.thinking_content) ||
        reasoningTextValue(delta.reasoning_details);
      if (reasoningDelta) {
        this.reasoning += reasoningDelta;
        this.onReasoningDelta?.(reasoningDelta);
      }
      for (const rawTool of Array.isArray(delta.tool_calls) ? delta.tool_calls : []) {
        const tool = record(rawTool);
        const index = numberValue(tool.index);
        const current = this.tools.get(index) ?? { id: "", name: "", arguments: "" };
        const fn = record(tool.function);
        current.id += textValue(tool.id);
        current.name += textValue(fn.name);
        current.arguments += textValue(fn.arguments);
        this.tools.set(index, current);
      }
      if (typeof choice.finish_reason === "string" && choice.finish_reason) {
        this.finishReason = choice.finish_reason;
      }
    }

    // Some providers send reasoning at the top level of the event (not inside
    // choices) without a separate event type.
    if (!choices.length) {
      const topReasoning =
        reasoningTextValue(event.reasoning) ||
        reasoningTextValue(event.reasoning_content) ||
        reasoningTextValue(event.thinking) ||
        reasoningTextValue(event.reasoning_text) ||
        reasoningTextValue(event.thinking_content);
      if (topReasoning) {
        this.reasoning += topReasoning;
        this.onReasoningDelta?.(topReasoning);
      }
    }
  }

  payload(): unknown {
    const toolCalls = [...this.tools.entries()].sort(([left], [right]) => left - right).map(([, tool]) => ({
      id: tool.id,
      type: "function",
      function: { name: tool.name, arguments: tool.arguments || "{}" },
    }));
    return {
      model: this.model,
      choices: [{
        finish_reason: this.finishReason,
        message: {
          content: this.text,
          ...(this.reasoning ? { reasoning_content: this.reasoning } : {}),
          ...(toolCalls.length > 0 ? { tool_calls: toolCalls } : {}),
        },
      }],
      ...(this.usage !== undefined ? { usage: this.usage } : {}),
    };
  }
}

class ResponsesAccumulator {
  private text = "";
  private reasoning = "";
  private model = "";
  private status = "";
  private usage: unknown;
  private readonly calls = new Map<string, { id: string; name: string; arguments: string }>();

  constructor(
    private readonly onTextDelta?: (delta: string) => void,
    private readonly onReasoningDelta?: (delta: string) => void,
  ) {}

  accept(value: unknown): { completed: boolean; finalPayload?: unknown } {
    const event = record(value);
    const type = textValue(event.type);
    if (type === "response.incomplete" || type === "response.failed" || type === "error") {
      throw new SseTransportError(`Responses SSE ended with ${type}`);
    }
    if (type === "response.completed") {
      return { completed: true, ...(event.response !== undefined ? { finalPayload: event.response } : {}) };
    }
    const response = record(event.response);
    if (typeof response.model === "string") this.model = response.model;
    if (typeof response.status === "string") this.status = response.status;
    if (response.usage !== undefined) this.usage = response.usage;
    if (type === "response.output_text.delta") {
      const delta = textValue(event.delta);
      this.text += delta;
      if (delta) this.onTextDelta?.(delta);
    }
    if (type === "response.reasoning_summary_text.delta" || type === "response.reasoning_text.delta" || type === "response.reasoning.delta") {
      const delta = reasoningTextValue(event.delta);
      this.reasoning += delta;
      if (delta) this.onReasoningDelta?.(delta);
    }
    if (type === "response.reasoning_summary_text.done" && !this.reasoning) this.reasoning = reasoningTextValue(event.text);
    const item = record(event.item);
    if (item.type === "function_call") this.registerCall(item);
    if (type === "response.function_call_arguments.delta") {
      const key = textValue(event.item_id) || textValue(event.call_id);
      const current = this.calls.get(key) ?? { id: key, name: textValue(event.name), arguments: "" };
      current.arguments += textValue(event.delta);
      this.calls.set(key, current);
    }
    if (type === "response.function_call_arguments.done") {
      const key = textValue(event.item_id) || textValue(event.call_id);
      const current = this.calls.get(key) ?? { id: key, name: textValue(event.name), arguments: "" };
      current.arguments = textValue(event.arguments) || current.arguments;
      this.calls.set(key, current);
    }
    return { completed: false };
  }

  payload(): unknown {
    const output: unknown[] = [];
    if (this.text) output.push({ type: "message", content: [{ type: "output_text", text: this.text }] });
    if (this.reasoning) output.push({ type: "reasoning", summary: [{ type: "summary_text", text: this.reasoning }] });
    for (const call of this.calls.values()) {
      output.push({ type: "function_call", call_id: call.id, name: call.name, arguments: call.arguments || "{}" });
    }
    return {
      model: this.model,
      status: this.status || "completed",
      output,
      ...(this.usage !== undefined ? { usage: this.usage } : {}),
    };
  }

  private registerCall(item: Record<string, unknown>): void {
    const key = textValue(item.id) || textValue(item.call_id);
    this.calls.set(key, {
      id: textValue(item.call_id) || key,
      name: textValue(item.name),
      arguments: textValue(item.arguments),
    });
  }
}

export function parseStreamingToolArguments(calls: readonly AgentToolCall[]): readonly AgentToolCall[] {
  return calls;
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function textValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/**
 * Extracts reasoning text from a value that may be a string, an array of
 * content blocks (e.g. `[{ type: "text", text: "..." }]`), or an object
 * with a `text` field. Returns "" for unrecognized shapes.
 */
function reasoningTextValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    return value.map((raw) => {
      const part = record(raw);
      return textValue(part.text) || textValue(part.content) || textValue(part.thinking);
    }).join("");
  }
  if (value && typeof value === "object") {
    const obj = value as Record<string, unknown>;
    return textValue(obj.text) || textValue(obj.content) || textValue(obj.thinking);
  }
  return "";
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isInteger(value) ? value : 0;
}
