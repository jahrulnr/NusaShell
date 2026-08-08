import type { AgentContentPart, AgentMessage, AgentTokenUsage, AgentToolArgumentError, AgentToolCall } from "@nusashell/application";
import type { ProviderApi } from "./openai-api-strategy.js";

export const transientStatuses = new Set([408, 409, 413, 425, 429, 500, 501, 502, 503, 504]);
export const DEFAULT_MAX_RESPONSE_BYTES = 8 * 1024 * 1024;
export const DEFAULT_TIMEOUT_MS = 60_000;
export const MAX_ATTACHMENT_BYTES = 4 * 1024 * 1024;
export const MAX_IMAGES_PER_MESSAGE = 4;
export const MAX_IMAGES_PER_CONTEXT = 8;

export class AgentProviderHttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly transient: boolean,
    readonly retryAfterMs: number,
    readonly kind: "http_status" | "connect" | "sse_transport" | "idle_timeout",
    override readonly cause?: unknown,
  ) {
    super(message, cause !== undefined ? { cause } : undefined);
    this.name = "AgentProviderHttpError";
  }
}

// --- Utility functions ---

export function validDataUrl(value: string, expectedMediaType: string): boolean {
  if (!value.startsWith("data:")) return false;
  const comma = value.indexOf(",");
  if (comma < 0) return false;
  const meta = value.slice(5, comma).toLowerCase();
  return meta.startsWith(expectedMediaType) || meta.startsWith(`${expectedMediaType.split("/")[0]};`);
}

export function parseDataUrl(value: string): { readonly mediaType: string; readonly base64: string } | null {
  if (!value.startsWith("data:")) return null;
  const comma = value.indexOf(",");
  if (comma < 0) return null;
  const meta = value.slice(5, comma);
  const isBase64 = meta.toLowerCase().endsWith(";base64");
  const mediaType = isBase64 ? meta.slice(0, -7) : meta;
  return { mediaType: mediaType || "application/octet-stream", base64: value.slice(comma + 1) };
}

export function emptySchema(): Record<string, unknown> {
  return { type: "object", properties: {}, additionalProperties: true };
}

export function safeSnippet(value: string): string {
  return value.length > 200 ? `${value.slice(0, 197)}...` : value;
}

export function clampInteger(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.trunc(value) || 0));
}

export function positiveInteger(value: number | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

export function integer(...values: unknown[]): number {
  for (const v of values) {
    if (typeof v === "number" && Number.isFinite(v)) return Math.max(0, Math.trunc(v));
  }
  return 0;
}

export function firstText(...values: unknown[]): string {
  for (const v of values) {
    if (typeof v === "string" && v.trim()) return v;
  }
  return "";
}

export function requireRecord(value: unknown, message: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(message);
  return value;
}

/**
 * Builds a non-transient AgentProviderHttpError for a 200 response whose body
 * is structurally invalid (missing choices/output/content). Includes a snippet
 * of the parsed payload so the caller (and logs) can see what the provider
 * actually returned instead of a generic "does not contain a completion choice".
 */
export function malformedResponseError(message: string, payload: unknown): AgentProviderHttpError {
  let snippet: string;
  try {
    snippet = safeSnippet(JSON.stringify(payload));
  } catch {
    snippet = safeSnippet(String(payload));
  }
  return new AgentProviderHttpError(
    `${message}: ${snippet}`,
    200,
    false,
    0,
    "http_status",
  );
}

export function record(value: unknown): Record<string, unknown> {
  return isRecord(value) ? value : {};
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// --- Shared parsing functions ---

export function parseToolCall(value: unknown): AgentToolCall {
  const call = requireRecord(value, "Provider returned an invalid tool call");
  const fn = requireRecord(call.function, "Provider returned an invalid tool call");
  if (typeof fn.name !== "string") throw new Error("Provider returned an invalid tool call");
  const argumentsResult = parseToolArgumentsWithRecovery(fn.arguments);
  return {
    id: typeof call.id === "string" ? call.id : `call_chat_${Math.random().toString(36).slice(2)}`,
    name: fn.name,
    args: argumentsResult.args,
    ...(argumentsResult.argumentError ? { argumentError: argumentsResult.argumentError } : {}),
  };
}

export function parseToolArguments(value: unknown): Record<string, unknown> {
  const result = parseToolArgumentsWithRecovery(value);
  if (result.argumentError) throw new Error("Provider returned invalid JSON tool arguments");
  return result.args;
}

export function parseToolArgumentsWithRecovery(value: unknown): {
  readonly args: Record<string, unknown>;
  readonly argumentError?: AgentToolArgumentError;
} {
  if (isRecord(value)) return { args: value };
  if (value === undefined || value === null) return { args: {} };
  if (typeof value !== "string") {
    return {
      args: {},
      argumentError: {
        code: "TOOL_ARGUMENTS_INVALID_JSON",
        message: "Tool call arguments were not a JSON object and were not executed. Re-issue this same tool call with exactly one JSON object matching its input schema.",
      },
    };
  }
  if (!value.trim()) return { args: {} };

  const direct = parseObject(value);
  if (direct) return { args: direct };

  for (const candidate of repairableJsonCandidates(value)) {
    const repaired = parseObject(candidate);
    if (repaired) return { args: repaired };
  }

  return {
    args: {},
    argumentError: {
      code: "TOOL_ARGUMENTS_INVALID_JSON",
      message: "Tool call arguments were not valid JSON and were not executed. Re-issue this same tool call with exactly one JSON object matching its input schema; do not include markdown fences, comments, or prose.",
    },
  };
}

function parseObject(value: string): Record<string, unknown> | undefined {
  try {
    const parsed = JSON.parse(value);
    return isRecord(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

/** Repairs only syntax wrappers; it never inserts missing values or brackets. */
function repairableJsonCandidates(value: string): readonly string[] {
  const trimmed = value.trim();
  const candidates = new Set<string>();
  const add = (candidate: string) => {
    const normalized = candidate.trim();
    if (normalized && normalized !== trimmed) candidates.add(normalized);
  };

  const fenced = trimmed.match(/^```(?:json)?\s*\n([\s\S]*?)\n?```$/i);
  if (fenced?.[1]) add(fenced[1]);
  try {
    const decoded = JSON.parse(trimmed);
    if (typeof decoded === "string") add(decoded);
  } catch {
    // The direct parse above already establishes that this is malformed.
  }
  for (const candidate of [...candidates, trimmed]) {
    add(stripTrailingCommas(candidate));
  }
  return [...candidates];
}

function stripTrailingCommas(value: string): string {
  let output = "";
  let quoted = false;
  let escaped = false;
  for (let index = 0; index < value.length; index += 1) {
    const char = value[index]!;
    if (quoted) {
      output += char;
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') quoted = false;
      continue;
    }
    if (char === '"') {
      quoted = true;
      output += char;
      continue;
    }
    if (char === ",") {
      let next = index + 1;
      while (next < value.length && /\s/.test(value[next]!)) next += 1;
      if (value[next] === "}" || value[next] === "]") continue;
    }
    output += char;
  }
  return output;
}

export function parseUsage(value: unknown): AgentTokenUsage | undefined {
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

export function extractContentText(value: unknown): string {
  if (typeof value === "string") return value;
  if (!Array.isArray(value)) return "";
  return value.map((raw) => {
    const part = record(raw);
    return ["text", "output_text", "summary_text", ""].includes(typeof part.type === "string" ? part.type : "")
      ? firstText(part.text)
      : "";
  }).join("");
}

// --- Shared content/attachment helpers ---

export function limitAttachments(parts: readonly AgentContentPart[], imageBudget: number): readonly AgentContentPart[] {
  let images = 0;
  return parts.filter((part) => {
    if (part.type === "text") return true;
    if (!validDataUrl(part.dataUrl, part.type === "image" ? "image/" : part.mediaType)) return false;
    if (part.type === "image" && (images >= Math.min(MAX_IMAGES_PER_MESSAGE, imageBudget) || ++images > MAX_IMAGES_PER_MESSAGE)) return false;
    return true;
  });
}

export function mapMessages(
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

// --- HTTP helpers ---

export function providerHeaders(api: ProviderApi, apiKey: string | undefined, stream: boolean): Record<string, string> {
  const base = {
    "content-type": "application/json",
    accept: stream ? "text/event-stream, application/json" : "application/json",
  };
  if (api === "messages") {
    return { ...base, "anthropic-version": "2023-06-01", ...(apiKey ? { "x-api-key": apiKey } : {}) };
  }
  return { ...base, ...(apiKey ? { authorization: `Bearer ${apiKey}` } : {}) };
}

export function timeoutSignal(timeoutMs: number, parent: AbortSignal | undefined): {
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

/**
 * Idle-reset timeout for SSE streaming. Unlike `timeoutSignal` (wall-clock),
 * this timer resets on every `reset()` call — ideally after each successful
 * `reader.read()` chunk. Fires only when the stream stalls for `timeoutMs`
 * with no data. Links to `parent` for user-cancel propagation.
 */
export function createIdleTimeout(timeoutMs: number, parent?: AbortSignal): {
  readonly signal: AbortSignal;
  readonly reset: () => void;
  readonly dispose: () => void;
  readonly timedOut: () => boolean;
} {
  const controller = new AbortController();
  let didTimeout = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const onParentAbort = () => controller.abort(parent?.reason);
  if (parent?.aborted) onParentAbort();
  else parent?.addEventListener("abort", onParentAbort, { once: true });
  const arm = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      didTimeout = true;
      controller.abort(new Error("Provider request timed out"));
    }, Math.max(1, timeoutMs));
  };
  arm();
  return {
    signal: controller.signal,
    reset: arm,
    timedOut: () => didTimeout,
    dispose: () => {
      if (timer) clearTimeout(timer);
      parent?.removeEventListener("abort", onParentAbort);
    },
  };
}

export async function readTextLimited(response: Response, maxBytes: number): Promise<string> {
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

// --- Stream/error classification helpers ---

export function looksLikeSse(response: Response): boolean {
  return response.headers.get("content-type")?.toLowerCase().includes("text/event-stream") === true;
}

export function looksLikeSseText(value: string): boolean {
  const prefix = value.trimStart().slice(0, 32).toLowerCase();
  return prefix.startsWith("data:") || prefix.startsWith("event:");
}

export function looksLikeChatCompletion(value: unknown): boolean {
  const payload = record(value);
  return payload.object === "chat.completion" || (Array.isArray(payload.choices) && !Array.isArray(payload.output));
}

export function isStreamUnsupported(status: number, body: string): boolean {
  if ([401, 402, 403].includes(status)) return false;
  const normalized = body.toLowerCase();
  return status >= 400 && status < 500
    && normalized.includes("stream")
    && ["not support", "unsupported", "disabled", "not available", "not enabled", "must be false", "non-stream"]
      .some((phrase) => normalized.includes(phrase));
}

export function isResponsesUnsupported(error: unknown): boolean {
  if (!(error instanceof AgentProviderHttpError)) return false;
  if (error.status === 404 || error.status === 405) return true;
  const normalized = error.message.toLowerCase();
  return error.status >= 400 && error.status < 600
    && ["not found", "not supported", "does not support", "unavailable"]
      .some((phrase) => normalized.includes(phrase));
}

export function looksLikeJsonStreamReject(value: unknown): boolean {
  const payload = record(value);
  const error = record(payload.error);
  return isStreamUnsupported(400, [error.message, error.code, error.type, payload.message].map(firstText).join(" "));
}

export function isTransientHttpStatus(status: number, body: string): boolean {
  if (!transientStatuses.has(status)) return false;
  return !isPermanentProviderFailure(status, body);
}

export function isPermanentProviderFailure(status: number, body: string): boolean {
  if (status === 402) return true;
  const normalized = body.toLowerCase();
  if ([
    "insufficient balance",
    "no resource package",
    "please recharge",
    "payment required",
    "out of credits",
    "credit balance",
    "billing",
    "top up",
    "top-up",
    "topup",
    "account suspended",
    "\"code\":\"1113\"",
    "\"code\":1113",
  ].some((phrase) => normalized.includes(phrase))) {
    return true;
  }
  return false;
}

export function isTransient(error: unknown): boolean {
  return error instanceof AgentProviderHttpError && error.transient;
}

export function shouldRetryWithoutImages(
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

export function retryAfterMs(error: unknown): number {
  return error instanceof AgentProviderHttpError ? error.retryAfterMs : 0;
}

export function retryDelay(
  retry: { readonly baseDelayMs?: number; readonly maxDelayMs?: number; readonly jitter?: number; readonly random?: () => number } | undefined,
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

export function parseRetryAfterMs(value: string | null, now = Date.now()): number {
  const normalized = value?.trim();
  if (!normalized) return 0;
  const seconds = Number(normalized);
  if (Number.isFinite(seconds)) return Math.max(0, seconds * 1000);
  const date = Date.parse(normalized);
  return Number.isFinite(date) ? Math.max(0, date - now) : 0;
}

export async function abortableSleep(delayMs: number, signal?: AbortSignal): Promise<void> {
  if (delayMs <= 0) return;
  await new Promise<void>((resolve) => {
    const onAbort = () => { clearTimeout(timer); resolve(); };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, delayMs);
    if (signal?.aborted) { clearTimeout(timer); resolve(); return; }
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
