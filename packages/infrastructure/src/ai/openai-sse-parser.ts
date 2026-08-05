import type { AgentToolCall } from "@nusashell/application";

export class SseTransportError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SseTransportError";
  }
}

export async function parseOpenAiSse(
  response: Response,
  api: "chat" | "responses" | "messages",
  onTextDelta: ((delta: string) => void) | undefined,
  onReasoningDelta: ((delta: string) => void) | undefined,
  maxBytes: number,
  onChunk?: () => void,
  idleSignal?: AbortSignal,
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
  const messages = new MessagesAccumulator(onTextDelta, onReasoningDelta);
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
    } else if (api === "messages") {
      const accepted = messages.accept(event, eventType);
      if (accepted.completed) completed = true;
      if (accepted.finalPayload !== undefined) finalPayload = accepted.finalPayload;
    } else {
      const accepted = responses.accept(event);
      if (accepted.completed) completed = true;
      if (accepted.finalPayload !== undefined) finalPayload = accepted.finalPayload;
    }
  };

  while (true) {
    let chunk;
    try {
      // Race reader.read() against the idle abort signal so a stalled
      // stream (no chunks for timeoutMs) rejects instead of hanging.
      if (idleSignal) {
        if (idleSignal.aborted) throw new SseTransportError("SSE read failed: Provider request timed out");
        const onAbort = () => { rejectRef(new Error("Provider request timed out")); };
        let rejectRef: (reason?: unknown) => void;
        const abortPromise = new Promise<never>((_, reject) => {
          rejectRef = reject;
          idleSignal.addEventListener("abort", onAbort, { once: true });
        });
        try {
          chunk = await Promise.race([reader.read(), abortPromise]);
        } finally {
          // Whether reader.read() won or the abort fired, detach the
          // listener so repeated reads don't accumulate abort listeners
          // on the shared idleSignal across iterations / turns.
          idleSignal.removeEventListener("abort", onAbort);
        }
      } else {
        chunk = await reader.read();
      }
    } catch (error) {
      throw new SseTransportError(`SSE read failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (chunk.done) break;
    // Reset the idle timer on every successful chunk — the stream is alive.
    onChunk?.();
    bytes += chunk.value.byteLength;
    if (bytes > maxBytes) throw new SseTransportError("SSE response exceeded the configured size limit");
    buffer += decoder.decode(chunk.value, { stream: true });
    const blocks = buffer.split(/\r?\n\r?\n/);
    buffer = blocks.pop() ?? "";
    for (const block of blocks) acceptBlock(block);
  }
  if (buffer.trim()) acceptBlock(buffer);

  if (!completed) throw new SseTransportError("SSE stream ended before completion");
  if (api === "chat") return finalPayload ?? chat.payload();
  if (api === "messages") return finalPayload ?? messages.payload();
  return finalPayload ?? responses.payload();
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

/**
 * Accumulator for Anthropic Messages API SSE stream format.
 *
 * Anthropic SSE uses typed events:
 * - `message_start` → message metadata
 * - `content_block_start` → new content block (text, thinking, tool_use)
 * - `content_block_delta` → incremental delta for a content block
 *   - `text_delta` → text content
 *   - `thinking_delta` → thinking/reasoning content
 *   - `input_json_delta` → partial tool use input JSON
 * - `content_block_stop` → content block complete
 * - `message_delta` → message-level delta (stop_reason, usage)
 * - `message_stop` → stream complete
 *
 * Some OpenAI-compatible proxies that accept Messages API body format
 * still return OpenAI Chat completions SSE format. In that case, the
 * ChatAccumulator would be used instead (sseMode = "chat").
 */
class MessagesAccumulator {
  private text = "";
  private reasoning = "";
  private model = "";
  private stopReason = "";
  private usage: unknown;
  private readonly contentBlocks = new Map<number, { type: string; text?: string; thinking?: string; toolUseId?: string; toolName?: string; toolInput?: string }>();
  completed = false;

  constructor(
    private readonly onTextDelta?: (delta: string) => void,
    private readonly onReasoningDelta?: (delta: string) => void,
  ) {}

  accept(value: unknown, eventType?: string): { completed: boolean; finalPayload?: unknown } {
    const event = record(value);
    const type = textValue(event.type) || eventType || "";

    if (type === "message_start") {
      const message = record(event.message);
      if (typeof message.model === "string") this.model = message.model;
      if (message.usage !== undefined) this.usage = message.usage;
      return { completed: false };
    }

    if (type === "content_block_start") {
      const index = numberValue(event.index);
      const block = record(event.content_block);
      // Store block metadata only (type, tool id/name). Do NOT store
      // initial text/thinking from content_block_start — some proxies
      // (e.g. Blackbox) include initial content here AND repeat it in
      // the first content_block_delta, which would double-count.
      // Deltas are the source of truth for streaming content.
      this.contentBlocks.set(index, {
        type: textValue(block.type),
        ...(typeof block.id === "string" ? { toolUseId: block.id } : {}),
        ...(typeof block.name === "string" ? { toolName: block.name } : {}),
      });
      return { completed: false };
    }

    if (type === "content_block_delta") {
      const index = numberValue(event.index);
      const delta = record(event.delta);
      const deltaType = textValue(delta.type);
      const block = this.contentBlocks.get(index) ?? { type: "" };

      if (deltaType === "text_delta") {
        const text = textValue(delta.text);
        if (text) {
          this.text += text;
          block.text = (block.text ?? "") + text;
          this.contentBlocks.set(index, block);
          this.onTextDelta?.(text);
        }
      } else if (deltaType === "thinking_delta") {
        const thinking = textValue(delta.thinking);
        if (thinking) {
          this.reasoning += thinking;
          block.thinking = (block.thinking ?? "") + thinking;
          this.contentBlocks.set(index, block);
          this.onReasoningDelta?.(thinking);
        }
      } else if (deltaType === "input_json_delta") {
        const partialJson = textValue(delta.partial_json);
        if (partialJson) {
          block.toolInput = (block.toolInput ?? "") + partialJson;
          this.contentBlocks.set(index, block);
        }
      }
      return { completed: false };
    }

    if (type === "content_block_stop") {
      // Content block complete — nothing to do, data already accumulated
      return { completed: false };
    }

    if (type === "message_delta") {
      const delta = record(event.delta);
      if (typeof delta.stop_reason === "string") this.stopReason = delta.stop_reason;
      if (event.usage !== undefined) this.usage = event.usage;
      return { completed: false };
    }

    if (type === "message_stop") {
      this.completed = true;
      return { completed: true, finalPayload: this.payload() };
    }

    if (type === "error") {
      const error = record(event.error);
      throw new SseTransportError(`Messages SSE error: ${textValue(error.message) || textValue(event.message) || "unknown"}`);
    }

    return { completed: false };
  }

  payload(): unknown {
    const content: Record<string, unknown>[] = [];
    const sortedBlocks = [...this.contentBlocks.entries()].sort(([a], [b]) => a - b);
    for (const [, block] of sortedBlocks) {
      if (block.type === "text" && block.text) {
        content.push({ type: "text", text: block.text });
      } else if ((block.type === "thinking" || block.type === "reasoning") && block.thinking) {
        content.push({ type: block.type, thinking: block.thinking });
      } else if (block.type === "tool_use" && block.toolName) {
        let input: unknown = {};
        try {
          input = block.toolInput ? JSON.parse(block.toolInput) : {};
        } catch {
          input = {};
        }
        content.push({ type: "tool_use", id: block.toolUseId ?? "", name: block.toolName, input });
      }
    }
    return {
      model: this.model,
      content,
      ...(this.stopReason ? { stop_reason: this.stopReason } : {}),
      ...(this.usage !== undefined ? { usage: this.usage } : {}),
    };
  }
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
