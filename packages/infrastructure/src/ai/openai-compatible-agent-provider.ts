import type {
  AgentProvider,
  AgentProviderRequest,
  AgentProviderResult,
} from "@nusashell/application";
import { resolveModelRuntimePolicy } from "./model-capability-policy.js";
import { parseOpenAiSse, SseTransportError } from "./openai-sse-parser.js";
import type { ApiStrategy, ProviderApi } from "./openai-api-strategy.js";
import { ChatApiStrategy, ResponsesApiStrategy, MessagesApiStrategy } from "./openai-api-strategy.js";
import {
  AgentProviderHttpError,
  abortableSleep,
  clampInteger,
  isResponsesUnsupported,
  isStreamUnsupported,
  isTransient,
  isTransientHttpStatus,
  looksLikeChatCompletion,
  looksLikeJsonStreamReject,
  looksLikeSse,
  looksLikeSseText,
  parseRetryAfterMs,
  providerHeaders,
  readTextLimited,
  retryAfterMs,
  retryDelay,
  safeSnippet,
  shouldRetryWithoutImages,
  timeoutSignal,
  DEFAULT_MAX_RESPONSE_BYTES,
  DEFAULT_TIMEOUT_MS,
} from "./openai-shared.js";

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
  readonly omitToolChoice?: boolean;
  readonly logger?: {
    warn(msg: string, ...args: unknown[]): void;
    info(msg: string, ...args: unknown[]): void;
  };
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

function createStrategy(api: ProviderApi): ApiStrategy {
  if (api === "responses") return new ResponsesApiStrategy();
  if (api === "messages") return new MessagesApiStrategy();
  return new ChatApiStrategy();
}

/** Chat Completions, Responses, and Anthropic Messages wire adapter. */
export class OpenAiCompatibleAgentProvider implements AgentProvider {
  readonly id: string;
  readonly managesAttemptBudget = true;
  private readonly endpoint: string;
  private readonly fetchFn: typeof fetch;
  private readonly strategy: ApiStrategy;

  constructor(private readonly options: OpenAiCompatibleAgentProviderOptions) {
    this.id = options.id ?? "openai-compatible";
    this.strategy = createStrategy(options.api ?? "chat");
    this.endpoint = `${options.baseUrl.replace(/\/+$/, "")}/${this.strategy.endpointPath}`;
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
    const stream = this.strategy.supportsStream && (this.options.stream ?? true);
    const allowVision = this.options.vision !== "off";
    const maxOutput = policy.maxOutput ?? this.options.maxOutputTokens;
    const body = this.strategy.buildBody(normalizedRequest, model, allowVision, maxOutput);
    if (this.options.omitToolChoice) delete (body as Record<string, unknown>).tool_choice;

    let payload: unknown;
    let usedStrategy: ApiStrategy = this.strategy;
    try {
      payload = await this.post(body, request, stream, true);
    } catch (error) {
      if (this.strategy.api === "responses" && isResponsesUnsupported(error) && !request.signal?.aborted) {
        this.options.logger?.warn("Agent provider falling back responses→chat provider=%s", this.id);
        const chatStrategy = new ChatApiStrategy();
        const chatBody = chatStrategy.buildBody(normalizedRequest, model, allowVision, maxOutput);
        if (this.options.omitToolChoice) delete (chatBody as Record<string, unknown>).tool_choice;
        const chatEndpoint = `${this.options.baseUrl.replace(/\/+$/, "")}/chat/completions`;
        try {
          payload = await this.post(chatBody, request, stream, true, chatEndpoint, "chat");
        } catch (chatError) {
          if (!shouldRetryWithoutImages(chatError, request.messages, request.signal)) throw chatError;
          this.options.logger?.warn("Agent provider falling back without images (chat) provider=%s", this.id);
          const fallbackChatBody = chatStrategy.buildBody(normalizedRequest, model, false, maxOutput);
          if (this.options.omitToolChoice) delete (fallbackChatBody as Record<string, unknown>).tool_choice;
          payload = await this.post(fallbackChatBody, request, stream, true, chatEndpoint, "chat");
        }
        usedStrategy = chatStrategy;
      } else {
        if (!shouldRetryWithoutImages(error, request.messages, request.signal)) throw error;
        this.options.logger?.warn("Agent provider falling back without images provider=%s api=%s", this.id, this.strategy.api);
        const fallbackBody = this.strategy.buildBody(normalizedRequest, model, false, maxOutput);
        if (this.options.omitToolChoice) delete (fallbackBody as Record<string, unknown>).tool_choice;
        payload = await this.post(fallbackBody, request, stream, true);
      }
    }
    const parsed = usedStrategy.api === "responses"
      ? looksLikeChatCompletion(payload)
        ? new ChatApiStrategy().parseResult(payload, model)
        : usedStrategy.parseResult(payload, model)
      : usedStrategy.parseResult(payload, model);
    return { ...parsed, providerId: this.id, api: usedStrategy.api };
  }

  private async post(
    body: Record<string, unknown>,
    request: AgentProviderRequest,
    stream: boolean,
    allowStreamFallback: boolean,
    overrideEndpoint?: string,
    overrideApi?: ProviderApi,
  ): Promise<unknown> {
    const endpoint = overrideEndpoint ?? this.endpoint;
    const api = overrideApi ?? this.strategy.api;
    body.stream = stream;
    const timeout = timeoutSignal(this.options.timeoutMs ?? DEFAULT_TIMEOUT_MS, request.signal);
    let response: Response;
    try {
      response = await this.fetchFn(endpoint, {
        method: "POST",
        headers: providerHeaders(api, this.options.apiKey, stream),
        body: JSON.stringify(body),
        signal: timeout.signal,
      });
    } catch (error) {
      timeout.dispose();
      if (timeout.timedOut()) {
        throw new AgentProviderHttpError(`Provider request timed out at ${endpoint}`, 0, true, 0, "connect", error);
      }
      if (request.signal?.aborted) {
        throw new AgentProviderHttpError(`Provider request was cancelled at ${endpoint}`, 0, false, 0, "connect", error);
      }
      throw new AgentProviderHttpError(`Provider connection failed at ${endpoint}`, 0, true, 0, "connect", error);
    }

    try {
      if (!response.ok) {
        const errorBody = await readTextLimited(response, Math.min(this.maxResponseBytes(), 4096));
        if (stream && allowStreamFallback && isStreamUnsupported(response.status, errorBody)) {
          this.options.logger?.warn("Agent provider falling back stream→non-stream provider=%s status=%d", this.id, response.status);
          return this.post(body, request, false, false, overrideEndpoint, overrideApi);
        }
        throw new AgentProviderHttpError(
          `Provider returned HTTP ${response.status}${errorBody ? `: ${safeSnippet(errorBody)}` : ""}`,
          response.status,
          isTransientHttpStatus(response.status, errorBody),
          parseRetryAfterMs(response.headers.get("retry-after")),
          "http_status",
        );
      }

      if (stream && looksLikeSse(response)) {
        try {
          return await parseOpenAiSse(
            response,
            api === "responses" ? "responses" : "chat",
            request.onTextDelta,
            request.onReasoningDelta,
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
        if (stream && allowStreamFallback) return this.post(body, request, false, false, overrideEndpoint, overrideApi);
        throw new AgentProviderHttpError("Provider returned an empty response body", response.status, false, 0, "http_status");
      }
      if (stream && looksLikeSseText(raw)) {
        return parseOpenAiSse(
          new Response(raw, { status: response.status, headers: { "content-type": "text/event-stream" } }),
          api === "responses" ? "responses" : "chat",
          request.onTextDelta,
          request.onReasoningDelta,
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
        return this.post(body, request, false, false, overrideEndpoint, overrideApi);
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

export { AgentProviderHttpError } from "./openai-shared.js";
