import type {
  AgentContentPart,
  AgentMessage,
  AgentProvider,
  AgentProviderRequest,
  AgentProviderResult,
  AgentTokenUsage,
  AgentToolCall,
} from "@nusashell/application";
import { resolveModelRuntimePolicy } from "./model-capability-policy.js";
import { parseOpenAiSse, SseTransportError } from "./openai-sse-parser.js";
import { mergeTextToolCalls } from "./text-tool-call-parser.js";

type ProviderApi = "chat" | "responses" | "messages";

export interface OpenAiCompatibleAgentProviderOptions {
  readonly id?: string;
  readonly api?: ProviderApi;
  readonly baseUrl: string;
  readonly apiKey?: string;
  readonly model?: string;
  readonly fetchFn?: typeof fetch;
  readonly stream?: boolean;
  readonly vision?: "auto" | "on" | "off";
  readonly timeoutMs?: number;
  readonly maxOutputTokens?: number;
  readonly maxResponseBytes?: number;
  readonly retry?: {
    readonly attemptBudget: number;
    readonly baseDelayMs: number;
    readonly maxDelayMs: number;
    readonly jitter: number;
    readonly random?: () => number;
    readonly sleep?: (delayMs: number, signal?: AbortSignal) => Promise<void>;
    readonly onRetry?: (event: {
      readonly providerId: string;
      readonly attempt: number;
      readonly delayMs: number;
      readonly status: number;
      readonly kind: "http_status" | "connect" | "sse_transport";
    }) => void;
  };
}

const transientStatuses = new Set([408, 409, 413, 425, 429, 500, 501, 502, 503, 504]);
const DEFAULT_MAX_RESPONSE_BYTES = 8 * 1024 * 1024;
const DEFAULT_TIMEOUT_MS = 60_000;
const MAX_ATTACHMENT_BYTES = 4 * 1024 * 1024;
const MAX_IMAGES_PER_MESSAGE = 4;
const MAX_IMAGES_PER_CONTEXT = 8;

/** Chat Completions, Responses, and Anthropic Messages wire adapter. */
export class OpenAiCompatibleAgentProvider implements AgentProvider {
  readonly id: string;
  readonly managesAttemptBudget = true;
  private readonly endpoint: string;
  private readonly fetchFn: typeof fetch;
  private readonly api: ProviderApi;

  constructor(private readonly options: OpenAiCompatibleAgentProviderOptions) {
    this.id = options.id ?? "openai-compatible";
    this.api = options.api ?? "chat";
    const path = this.api === "responses" ? "responses" : this.api === "messages" ? "messages" : "chat/completions";
    this.endpoint = `${options.baseUrl.replace(/\/+$/, "")}/${path}`;
    this.fetchFn = options.fetchFn ?? fetch;
  }

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const budget = clampInteger(this.options.retry?.attemptBudget ?? 1, 1, 10);
    let lastError: unknown;
    for (let attempt = 1; attempt <= budget; attempt += 1) {
      if (request.consumeAttempt && !request.consumeAttempt()) {
        throw lastError ?? new AgentProviderHttpError(
          "Agent provider attempt budget exhausted",
          0,
          true,
          0,
          "connect",
        );
      }
      try {
        return await this.completeOnce(request);
      } catch (error) {
        lastError = error;
        if (!isTransient(error) || attempt >= budget || request.signal?.aborted) throw error;
        const delayMs = retryDelay(this.options.retry, attempt, retryAfterMs(error));
        this.options.retry?.onRetry?.({
          providerId: this.id,
          attempt,
          delayMs,
          status: error instanceof AgentProviderHttpError ? error.status : 0,
          kind: error instanceof AgentProviderHttpError ? error.kind : "connect",
        });
        await (this.options.retry?.sleep ?? abortableSleep)(delayMs, request.signal);
      }
    }
    throw lastError;
  }

  private async completeOnce(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const model = request.model ?? this.options.model;
    if (!model) throw new Error("Select a model before starting an agent turn");
    const policy = resolveModelRuntimePolicy({
      model,
      ...(request.effort ? { requestedEffort: request.effort } : {}),
      ...(request.modelCapabilities ? { capabilities: request.modelCapabilities } : {}),
    });
    const tools = policy.supportsTools ? request.tools : [];
    const normalizedRequest: AgentProviderRequest = {
      ...request,
      tools,
      ...(policy.effort ? { effort: policy.effort } : { effort: "auto" }),
    };
    const stream = this.api !== "messages" && (this.options.stream ?? true);
    const allowVision = this.options.vision !== "off";
    const body = this.api === "responses"
      ? toResponsesBody(normalizedRequest, model, allowVision, policy.maxOutput ?? this.options.maxOutputTokens)
      : this.api === "messages"
        ? toMessagesBody(normalizedRequest, model, allowVision, policy.maxOutput ?? this.options.maxOutputTokens)
        : toChatBody(normalizedRequest, model, allowVision, policy.maxOutput ?? this.options.maxOutputTokens);

    let payload: unknown;
    try {
      payload = await this.post(body, request, stream, true);
    } catch (error) {
      if (!shouldRetryWithoutImages(error, request.messages, request.signal)) throw error;
      const fallbackBody = this.api === "responses"
        ? toResponsesBody(normalizedRequest, model, false, policy.maxOutput ?? this.options.maxOutputTokens)
        : this.api === "messages"
          ? toMessagesBody(normalizedRequest, model, false, policy.maxOutput ?? this.options.maxOutputTokens)
          : toChatBody(normalizedRequest, model, false, policy.maxOutput ?? this.options.maxOutputTokens);
      payload = await this.post(fallbackBody, request, stream, true);
    }
    const parsed = this.api === "responses"
      ? looksLikeChatCompletion(payload)
        ? parseChatResult(payload, model)
        : parseResponsesResult(payload, model)
      : this.api === "messages"
        ? parseMessagesResult(payload, model)
        : parseChatResult(payload, model);
    return { ...parsed, providerId: this.id, api: this.api };
  }

  private async post(
    body: Record<string, unknown>,
    request: AgentProviderRequest,
    stream: boolean,
    allowStreamFallback: boolean,
  ): Promise<unknown> {
    body.stream = stream;
    const timeout = timeoutSignal(this.options.timeoutMs ?? DEFAULT_TIMEOUT_MS, request.signal);
    let response: Response;
    try {
      response = await this.fetchFn(this.endpoint, {
        method: "POST",
        headers: providerHeaders(this.api, this.options.apiKey, stream),
        body: JSON.stringify(body),
        signal: timeout.signal,
      });
    } catch (error) {
      timeout.dispose();
      if (timeout.timedOut()) {
        throw new AgentProviderHttpError("Provider request timed out", 0, true, 0, "connect", error);
      }
      if (request.signal?.aborted) {
        throw new AgentProviderHttpError("Provider request was cancelled", 0, false, 0, "connect", error);
      }
      throw new AgentProviderHttpError("Provider connection failed", 0, true, 0, "connect", error);
    }

    try {
      if (!response.ok) {
        const errorBody = await readTextLimited(response, Math.min(this.maxResponseBytes(), 4096));
        if (stream && allowStreamFallback && isStreamUnsupported(response.status, errorBody)) {
          return this.post(body, request, false, false);
        }
        throw new AgentProviderHttpError(
          `Provider returned HTTP ${response.status}${errorBody ? `: ${safeSnippet(errorBody)}` : ""}`,
          response.status,
          transientStatuses.has(response.status),
          parseRetryAfterMs(response.headers.get("retry-after")),
          "http_status",
        );
      }

      if (stream && looksLikeSse(response)) {
        try {
          return await parseOpenAiSse(
            response,
            this.api === "responses" ? "responses" : "chat",
            request.onTextDelta,
            this.maxResponseBytes(),
          );
        } catch (error) {
          if (error instanceof SseTransportError) {
            throw new AgentProviderHttpError(error.message, response.status, true, 0, "sse_transport", error);
          }
          throw error;
        }
      }

      const raw = await readTextLimited(response, this.maxResponseBytes());
      if (!raw.trim()) {
        if (stream && allowStreamFallback) return this.post(body, request, false, false);
        throw new AgentProviderHttpError("Provider returned an empty response body", response.status, false, 0, "http_status");
      }
      if (stream && looksLikeSseText(raw)) {
        return parseOpenAiSse(
          new Response(raw, { status: response.status, headers: { "content-type": "text/event-stream" } }),
          this.api === "responses" ? "responses" : "chat",
          request.onTextDelta,
          this.maxResponseBytes(),
        );
      }
      let payload: unknown;
      try {
        payload = JSON.parse(raw);
      } catch (error) {
        throw new AgentProviderHttpError(
          `Provider returned invalid JSON: ${safeSnippet(raw)}`,
          response.status,
          false,
          0,
          "http_status",
          error,
        );
      }
      if (stream && allowStreamFallback && looksLikeJsonStreamReject(payload)) {
        return this.post(body, request, false, false);
      }
      return payload;
    } finally {
      timeout.dispose();
    }
  }

  private maxResponseBytes(): number {
    return clampInteger(this.options.maxResponseBytes ?? DEFAULT_MAX_RESPONSE_BYTES, 1024, 32 * 1024 * 1024);
  }
}

export class AgentProviderHttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly transient: boolean,
    readonly retryAfterMs: number,
    readonly kind: "http_status" | "connect" | "sse_transport",
    override readonly cause?: unknown,
  ) {
    super(message, cause !== undefined ? { cause } : undefined);
    this.name = "AgentProviderHttpError";
  }
}

function toChatBody(
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

function toResponsesBody(
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
      input.push({ type: "function_call_output", call_id: message.toolCallId, output: message.content });
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
  return {
    model,
    input,
    ...(instructions.length > 0 ? { instructions: instructions.join("\n\n") } : {}),
    ...(request.tools.length > 0 ? {
      tools: request.tools.map(toResponsesTool),
      tool_choice: "auto",
    } : {}),
    ...(positiveInteger(maxOutputTokens) ? { max_output_tokens: maxOutputTokens } : {}),
    ...responsesEffort(request.effort),
  };
}

function toMessagesBody(
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

function mapMessages(
  messages: readonly AgentMessage[],
  mapUserParts: (parts: readonly AgentContentPart[]) => unknown,
): readonly Record<string, unknown>[] {
  let contextImages = 0;
  return messages.map((message) => {
    if (message.role === "tool") {
      return { role: "tool", tool_call_id: message.toolCallId, content: message.content };
    }
    if (message.role === "assistant") {
      return {
        role: "assistant",
        ...(message.content ? { content: message.content } : {}),
        ...(message.toolCalls ? {
          tool_calls: message.toolCalls.map((call) => ({
            id: call.id,
            type: "function",
            function: { name: call.name, arguments: JSON.stringify(call.args) },
          })),
        } : {}),
      };
    }
    if (message.role === "system") return { role: "system", content: message.content };
    if (typeof message.content === "string") return { role: "user", content: message.content };
    const limited = limitAttachments(message.content, MAX_IMAGES_PER_CONTEXT - contextImages);
    contextImages += limited.filter((part) => part.type === "image").length;
    return { role: "user", content: mapUserParts(limited) };
  });
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

function limitAttachments(parts: readonly AgentContentPart[], imageBudget: number): readonly AgentContentPart[] {
  let images = 0;
  return parts.filter((part) => {
    if (part.type === "text") return true;
    if (!validDataUrl(part.dataUrl, part.type === "image" ? "image/" : part.mediaType)) return false;
    if (part.type === "image" && (images >= Math.min(MAX_IMAGES_PER_MESSAGE, imageBudget) || ++images > MAX_IMAGES_PER_MESSAGE)) return false;
    return true;
  });
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

function toResponsesTool(tool: AgentProviderRequest["tools"][number]): Record<string, unknown> {
  return {
    type: "function",
    name: tool.name,
    ...(tool.description ? { description: tool.description } : {}),
    parameters: tool.inputSchema ?? emptySchema(),
  };
}

function chatEffort(effort: AgentProviderRequest["effort"]): Record<string, unknown> {
  return effort && effort !== "auto"
    ? { reasoning_effort: effort, reasoning: { effort } }
    : {};
}

function responsesEffort(effort: AgentProviderRequest["effort"]): Record<string, unknown> {
  return effort && effort !== "auto" ? { reasoning: { effort } } : {};
}

function parseChatResult(payload: unknown, fallbackModel: string): AgentProviderResult {
  const root = requireRecord(payload, "Provider response is not an object");
  const choices = Array.isArray(root.choices) ? root.choices : [];
  const choice = requireRecord(choices[0], "Provider response does not contain a completion choice");
  const message = requireRecord(choice.message, "Provider response does not contain a completion message");
  const nativeCalls = Array.isArray(message.tool_calls) ? message.tool_calls.map(parseToolCall) : [];
  const merged = mergeTextToolCalls(nativeCalls, extractContentText(message.content));
  const reasoning = firstText(message.reasoning_content, message.reasoning, message.thinking);
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

function parseResponsesResult(payload: unknown, fallbackModel: string): AgentProviderResult {
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

function parseMessagesResult(payload: unknown, fallbackModel: string): AgentProviderResult {
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

function parseToolCall(value: unknown): AgentToolCall {
  const call = requireRecord(value, "Provider returned an invalid tool call");
  const fn = requireRecord(call.function, "Provider returned an invalid tool call");
  if (typeof fn.name !== "string") throw new Error("Provider returned an invalid tool call");
  return {
    id: typeof call.id === "string" ? call.id : `call_chat_${Math.random().toString(36).slice(2)}`,
    name: fn.name,
    args: parseToolArguments(fn.arguments),
  };
}

function parseToolArguments(value: unknown): Record<string, unknown> {
  if (isRecord(value)) return value;
  if (typeof value !== "string" || !value.trim()) return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error("Provider returned invalid JSON tool arguments");
  }
  if (!isRecord(parsed)) throw new Error("Provider tool arguments must be an object");
  return parsed;
}

function parseUsage(value: unknown): AgentTokenUsage | undefined {
  if (!isRecord(value)) return undefined;
  const usage = record(value);
  const promptDetails = record(usage.prompt_tokens_details);
  const inputDetails = record(usage.input_tokens_details);
  const outputDetails = record(usage.completion_tokens_details);
  const responsesOutputDetails = record(usage.output_tokens_details);
  return {
    inputTokens: integer(usage.prompt_tokens, usage.input_tokens),
    outputTokens: integer(usage.completion_tokens, usage.output_tokens),
    cachedInputTokens: integer(promptDetails.cached_tokens, inputDetails.cached_tokens, usage.cache_read_input_tokens),
    cacheWriteTokens: integer(promptDetails.cache_write_tokens, inputDetails.cache_write_tokens, usage.cache_creation_input_tokens),
    reasoningOutputTokens: integer(outputDetails.reasoning_tokens, responsesOutputDetails.reasoning_tokens),
  };
}

function extractContentText(value: unknown): string {
  if (typeof value === "string") return value;
  if (!Array.isArray(value)) return "";
  return value.map((raw) => {
    const part = record(raw);
    return ["text", "output_text", "summary_text", ""].includes(typeof part.type === "string" ? part.type : "")
      ? firstText(part.text)
      : "";
  }).join("");
}

function providerHeaders(api: ProviderApi, apiKey: string | undefined, stream: boolean): Record<string, string> {
  const base = {
    "content-type": "application/json",
    accept: stream ? "text/event-stream, application/json" : "application/json",
  };
  if (api === "messages") {
    return { ...base, "anthropic-version": "2023-06-01", ...(apiKey ? { "x-api-key": apiKey } : {}) };
  }
  return { ...base, ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) };
}

function timeoutSignal(timeoutMs: number, parent: AbortSignal | undefined): {
  readonly signal: AbortSignal;
  readonly dispose: () => void;
  readonly timedOut: () => boolean;
} {
  const controller = new AbortController();
  let didTimeout = false;
  const onParentAbort = () => controller.abort(parent?.reason);
  if (parent?.aborted) onParentAbort();
  else parent?.addEventListener("abort", onParentAbort, { once: true });
  const timer = setTimeout(() => {
    didTimeout = true;
    controller.abort(new Error("Provider request timed out"));
  }, Math.max(1, timeoutMs));
  return {
    signal: controller.signal,
    timedOut: () => didTimeout,
    dispose: () => {
      clearTimeout(timer);
      parent?.removeEventListener("abort", onParentAbort);
    },
  };
}

async function readTextLimited(response: Response, maxBytes: number): Promise<string> {
  if (!response.body) return "";
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let total = 0;
  let output = "";
  while (true) {
    const chunk = await reader.read();
    if (chunk.done) break;
    total += chunk.value.byteLength;
    if (total > maxBytes) throw new AgentProviderHttpError(
      "Provider response exceeded the configured size limit",
      response.status,
      false,
      0,
      "http_status",
    );
    output += decoder.decode(chunk.value, { stream: true });
  }
  return output + decoder.decode();
}

function looksLikeSse(response: Response): boolean {
  return response.headers.get("content-type")?.toLowerCase().includes("text/event-stream") === true;
}

function looksLikeSseText(value: string): boolean {
  const prefix = value.trimStart().slice(0, 32).toLowerCase();
  return prefix.startsWith("data:") || prefix.startsWith("event:");
}

function looksLikeChatCompletion(value: unknown): boolean {
  const payload = record(value);
  return payload.object === "chat.completion" || (Array.isArray(payload.choices) && !Array.isArray(payload.output));
}

function isStreamUnsupported(status: number, body: string): boolean {
  if ([401, 402, 403].includes(status)) return false;
  const normalized = body.toLowerCase();
  return status >= 400 && status < 500
    && normalized.includes("stream")
    && ["not support", "unsupported", "disabled", "not available", "not enabled", "must be false", "non-stream"]
      .some((phrase) => normalized.includes(phrase));
}

function looksLikeJsonStreamReject(value: unknown): boolean {
  const payload = record(value);
  const error = record(payload.error);
  return isStreamUnsupported(400, [error.message, error.code, error.type, payload.message].map(firstText).join(" "));
}

function isTransient(error: unknown): boolean {
  return error instanceof AgentProviderHttpError && error.transient;
}

function shouldRetryWithoutImages(
  error: unknown,
  messages: readonly AgentMessage[],
  signal: AbortSignal | undefined,
): boolean {
  return !signal?.aborted
    && error instanceof AgentProviderHttpError
    && error.status >= 400
    && error.status < 500
    && messages.some((message) => message.role === "user"
      && Array.isArray(message.content)
      && message.content.some((part) => part.type === "image"));
}

function retryAfterMs(error: unknown): number {
  return error instanceof AgentProviderHttpError ? error.retryAfterMs : 0;
}

function retryDelay(
  retry: OpenAiCompatibleAgentProviderOptions["retry"],
  retryNumber: number,
  providerDelayMs: number,
): number {
  const base = Math.max(0, retry?.baseDelayMs ?? 250);
  const max = Math.max(base, retry?.maxDelayMs ?? 5000);
  if (providerDelayMs > 0) return Math.min(providerDelayMs, max);
  const exponential = Math.min(max, base * (2 ** Math.min(20, Math.max(0, retryNumber - 1))));
  const jitter = Math.min(1, Math.max(0, retry?.jitter ?? 0.2));
  const random = retry?.random?.() ?? Math.random();
  return Math.max(0, Math.min(max, Math.round(exponential * (1 + jitter * (2 * random - 1)))));
}

function parseRetryAfterMs(value: string | null, now = Date.now()): number {
  const normalized = value?.trim();
  if (!normalized) return 0;
  const seconds = Number(normalized);
  if (Number.isFinite(seconds)) return Math.max(0, seconds * 1000);
  const date = Date.parse(normalized);
  return Number.isFinite(date) ? Math.max(0, date - now) : 0;
}

function abortableSleep(delayMs: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const timer = setTimeout(resolve, delayMs);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(signal.reason);
    }, { once: true });
  });
}

function validDataUrl(value: string, expectedMediaType: string): boolean {
  const parsed = parseDataUrl(value);
  return parsed !== null
    && parsed.mediaType.toLowerCase().startsWith(expectedMediaType.toLowerCase())
    && Math.floor(parsed.base64.length * 0.75) <= MAX_ATTACHMENT_BYTES;
}

function parseDataUrl(value: string): { readonly mediaType: string; readonly base64: string } | null {
  const match = /^data:([^;,]+);base64,([a-z0-9+/=\r\n]+)$/i.exec(value);
  return match ? { mediaType: match[1] ?? "", base64: (match[2] ?? "").replace(/\s/g, "") } : null;
}

function emptySchema(): Record<string, unknown> {
  return { type: "object", properties: {} };
}

function safeSnippet(value: string): string {
  return value.trim().replace(/\s+/g, " ").slice(0, 800);
}

function clampInteger(value: number, min: number, max: number): number {
  return Number.isInteger(value) ? Math.min(max, Math.max(min, value)) : min;
}

function positiveInteger(value: number | undefined): value is number {
  return Number.isInteger(value) && (value ?? 0) > 0;
}

function integer(...values: unknown[]): number {
  for (const value of values) {
    if (typeof value === "number" && Number.isFinite(value)) return Math.max(0, Math.trunc(value));
  }
  return 0;
}

function firstText(...values: unknown[]): string {
  return values.find((value): value is string => typeof value === "string") ?? "";
}

function requireRecord(value: unknown, message: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(message);
  return value;
}

function record(value: unknown): Record<string, unknown> {
  return isRecord(value) ? value : {};
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
