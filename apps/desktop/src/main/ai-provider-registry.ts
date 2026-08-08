import type {
  AgentModelOption,
  AiModelSettings,
  AiProviderApi,
  AiProviderSettings,
  AiProviderType,
  AiRegistrySettings,
  ReasoningEffort,
} from "../shared/ai-contract.js";
import { inferProviderType, providerDefinition } from "./ai-provider-definitions.js";

export type {
  AgentModelOption,
  AiModelSettings,
  AiProviderSettings,
  AiRegistrySettings,
  ReasoningEffort,
} from "../shared/ai-contract.js";

const efforts: readonly ReasoningEffort[] = ["auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"];

/**
 * Fresh-install / missing-key defaults for the compaction cost ceiling.
 * Existing saved `ai-settings.json` values are preserved by `normalizeRegistryState`
 * (the default only applies when the key is absent or invalid). These defaults
 * match the cheap-agentic p10 model window (200k) so long tasks are usable
 * out of the box without forcing every user to open Settings.
 */
export const DEFAULT_MAX_INPUT_TOKENS = 200_000;
export const DEFAULT_RESERVE_TOKENS = 16_000;

const nonChatTasks = new Set([
  "embedding", "embeddings", "text-to-speech", "speech-to-text", "tts", "stt",
  "transcription", "translation", "image-generation", "video-generation",
  "moderation", "rerank", "reranking", "classification", "ocr",
]);
const nonChatMarkers = [
  "embedding", "embed-", "-embed", "/embed", "rerank", "re-rank", "moderation",
  "transcribe", "transcription", "whisper", "text-to-speech", "speech-to-text",
  "-tts", "/tts", "tts-", "-stt", "/stt", "stt-", "gpt-image", "dall-e",
  "image-generation", "imagegen", "stable-diffusion", "sdxl", "video-generation",
];

export function normalizeRegistryState(raw: unknown): AiRegistrySettings {
  const value = record(raw);
  if (Array.isArray(value.providers)) {
    const providers = value.providers.map(normalizeProvider).filter((provider): provider is AiProviderSettings => provider !== null);
    const requestedProviderId = text(value.activeProviderId);
    const activeProviderId = providers.some((provider) => provider.id === requestedProviderId)
      ? requestedProviderId
      : providers[0]?.id ?? "";
    return {
      activeProviderId,
      activeModelKey: text(value.activeModelKey),
      effort: normalizeEffort(value.effort),
      strategy: normalizeStrategy(value.strategy),
      totalAttemptBudget: integerInRange(value.totalAttemptBudget, 1, 32, 4),
      stream: value.stream !== false,
      vision: normalizeVision(value.vision),
      userPrompt: text(value.userPrompt),
      maxToolRounds: integerInRange(value.maxToolRounds, 0, 10_000, 50),
      maxRepeatedToolCalls: integerInRange(value.maxRepeatedToolCalls, 1, 200, 50),
      maxAutoContinues: integerInRange(value.maxAutoContinues, 0, 10_000, 10),
      compactionEnabled: value.compactionEnabled !== false,
      maxInputTokens: integerInRange(value.maxInputTokens, 1000, 2_000_000, DEFAULT_MAX_INPUT_TOKENS),
      reserveTokens: integerInRange(value.reserveTokens, 0, 1_000_000, DEFAULT_RESERVE_TOKENS),
      recentTurns: integerInRange(value.recentTurns, 1, 100, 4),
      summaryMaxChars: integerInRange(value.summaryMaxChars, 100, 1_000_000, 12000),
      providers,
    };
  }

  if (value.providerId === "openai-compatible") {
    const model = text(value.model);
    return {
      activeProviderId: "openai-compatible",
      activeModelKey: model ? `openai-compatible::${model}` : "",
      effort: normalizeEffort(value.effort),
      strategy: "failover",
      totalAttemptBudget: 4,
      stream: true,
      vision: "auto",
      userPrompt: text(value.userPrompt),
      maxToolRounds: integerInRange(value.maxToolRounds, 0, 10_000, 50),
      maxRepeatedToolCalls: integerInRange(value.maxRepeatedToolCalls, 1, 200, 50),
      maxAutoContinues: integerInRange(value.maxAutoContinues, 0, 10_000, 10),
      compactionEnabled: value.compactionEnabled !== false,
      maxInputTokens: integerInRange(value.maxInputTokens, 1000, 2_000_000, DEFAULT_MAX_INPUT_TOKENS),
      reserveTokens: integerInRange(value.reserveTokens, 0, 1_000_000, DEFAULT_RESERVE_TOKENS),
      recentTurns: integerInRange(value.recentTurns, 1, 100, 4),
      summaryMaxChars: integerInRange(value.summaryMaxChars, 100, 1_000_000, 12000),
      providers: [{
        id: "openai-compatible",
        name: "OpenAI compatible",
        type: "openai-compatible",
        api: "chat",
        baseUrl: text(value.baseUrl),
        ...(text(value.apiKey) ? { apiKey: text(value.apiKey) } : {}),
        apiKeyOptional: false,
        enabled: true,
        defaultModel: model,
        timeoutMs: 60_000,
        maxAttempts: 1,
        weight: 1,
        models: model ? [basicModel(model)] : [],
      }],
    };
  }

  return {
    activeProviderId: "",
    activeModelKey: "",
    effort: normalizeEffort(value.effort),
    strategy: "failover",
    totalAttemptBudget: 4,
    stream: true,
    vision: "auto",
    userPrompt: text(value.userPrompt),
    maxToolRounds: integerInRange(value.maxToolRounds, 0, 10_000, 50),
    maxRepeatedToolCalls: integerInRange(value.maxRepeatedToolCalls, 1, 200, 50),
    maxAutoContinues: integerInRange(value.maxAutoContinues, 0, 10_000, 10),
    compactionEnabled: value.compactionEnabled !== false,
    maxInputTokens: integerInRange(value.maxInputTokens, 1000, 2_000_000, DEFAULT_MAX_INPUT_TOKENS),
    reserveTokens: integerInRange(value.reserveTokens, 0, 1_000_000, DEFAULT_RESERVE_TOKENS),
    recentTurns: integerInRange(value.recentTurns, 1, 100, 4),
    summaryMaxChars: integerInRange(value.summaryMaxChars, 100, 1_000_000, 12000),
    providers: [],
  };
}

export function flattenModelCatalog(providers: readonly AiProviderSettings[]): readonly AgentModelOption[] {
  return providers
    .filter((provider) => provider.enabled)
    .flatMap((provider) => provider.models
      .filter(isChatSelectable)
      .map((model) => ({
        ...model,
        key: `${provider.id}::${model.id}`,
        providerId: provider.id,
        providerName: provider.name,
      })));
}

/**
 * Resolve the model that should drive backend fallbacks (ticket #39).
 *
 * The composer picker's global active model (`activeModelKey`) is the shell's
 * single source of truth for "model the shell runs by default" — used by
 * scheduled job/pipeline agent turns and their compaction summarizer, which
 * otherwise fall back to the provider's own default (`ConfigureAiCommand.model`)
 * set at bootstrap. Returns the provider id + model id for that active model so
 * the IPC layer can re-configure the backend provider default whenever the
 * global picker changes.
 *
 * @returns {{ providerId: string, model: string } | null} null when no active model is set.
 */
export function resolveActiveModelDefault(
  providers: readonly AiProviderSettings[],
  activeModelKey: string,
): { providerId: string; model: string } | null {
  if (!activeModelKey) return null;
  const selected = flattenModelCatalog(providers).find((model) => model.key === activeModelKey);
  if (!selected) return null;
  return { providerId: selected.providerId, model: selected.id };
}

export async function importProviderModels(
  provider: AiProviderSettings,
  fetchFn: typeof fetch = fetch,
): Promise<readonly AiModelSettings[]> {
  const baseUrl = provider.baseUrl.trim().replace(/\/+$/, "");
  if (!baseUrl) throw new Error("Provider base URL is required before importing models");

  const isLocal = provider.type === "ollama" || provider.type === "llamacpp";
  const importTimeout = isLocal ? 180_000 : 30_000;
  let target = new URL(`${baseUrl}/models`);
  const origin = target.origin;
  const visited = new Set<string>();
  const seen = new Set<string>();
  const models: AiModelSettings[] = [];

  try {
    for (let page = 0; page < 10; page += 1) {
      if (visited.has(target.toString())) break;
      visited.add(target.toString());
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(new Error("Models import timed out")), importTimeout);
      let response: Response;
      let payload: unknown;
      try {
        response = await fetchFn(target.toString(), {
          method: "GET",
          headers: {
            accept: "application/json",
            ...(provider.apiKey ? { authorization: `Bearer ${provider.apiKey}`, "x-api-key": provider.apiKey } : {}),
            ...(provider.api === "messages" ? { "anthropic-version": "2023-06-01" } : {}),
          },
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Models endpoint returned HTTP ${response.status}`);
        payload = await readJsonLimited(response, 16 * 1024 * 1024);
      } finally {
        clearTimeout(timer);
      }
      const parsed = parseModelPage(payload);
      for (const item of parsed.items) {
        const raw = record(item);
        if (text(raw.object) && text(raw.object).toLowerCase() !== "model") continue;
        if (text(raw.type) && text(raw.type).toLowerCase() !== "model") continue;
        const model = normalizeImportedModel(item);
        if (!model || seen.has(model.id)) continue;
        seen.add(model.id);
        models.push(model);
      }
      if (!parsed.next) break;

      const next = new URL(parsed.next, target);
      if (next.origin !== origin) throw new Error("Models pagination cannot leave the provider origin");
      target = next;
    }
  } catch (error) {
    if (provider.type === "ollama") {
      const fallback = await importOllamaTags(origin, provider, fetchFn, importTimeout);
      if (fallback.length > 0) {
        for (const model of fallback) {
          if (!seen.has(model.id)) { seen.add(model.id); models.push(model); }
        }
        await enrichOllamaModels(models, origin, provider, fetchFn);
        return models.sort((left, right) => left.id.localeCompare(right.id));
      }
      throw wrapLocalImportError(error, provider, baseUrl);
    }
    if (provider.type === "llamacpp") throw wrapLocalImportError(error, provider, baseUrl);
    throw error;
  }

  if (provider.type === "ollama" && models.length > 0) {
    await enrichOllamaModels(models, origin, provider, fetchFn);
  }
  if (provider.type === "llamacpp" && models.length > 0) {
    await enrichLlamaCppModels(models, origin, provider, fetchFn);
  }

  if (isLocal && models.length === 0) {
    throw new Error(
      provider.type === "ollama"
        ? `No models reported by Ollama at ${baseUrl}. Pull a model with \`ollama pull <name>\` then retry Import.`
        : `No models reported by llama.cpp at ${baseUrl}. For single-model mode pass \`-m model.gguf\`; for router use \`--models-dir\`.`,
    );
  }

  return models.sort((left, right) => left.id.localeCompare(right.id));
}

function wrapLocalImportError(error: unknown, provider: AiProviderSettings, baseUrl: string): Error {
  const message = error instanceof Error ? error.message : String(error);
  if (provider.type === "ollama") {
    return new Error(
      `Cannot reach Ollama at ${baseUrl}. Is \`ollama serve\` running? (${message})`,
    );
  }
  if (provider.type === "llamacpp") {
    if (/401|auth|key/i.test(message)) {
      return new Error(`llama.cpp API key rejected at ${baseUrl}. Clear the key or match \`--api-key\`. (${message})`);
    }
    return new Error(
      `Cannot reach llama.cpp at ${baseUrl}. Start the server (e.g. \`llama-server -m model.gguf --port 8080 --jinja\`) or fix Base URL. (${message})`,
    );
  }
  return error instanceof Error ? error : new Error(message);
}

async function importOllamaTags(
  origin: string,
  provider: AiProviderSettings,
  fetchFn: typeof fetch,
  timeoutMs: number,
): Promise<AiModelSettings[]> {
  const tagsUrl = `${origin}/api/tags`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error("Ollama /api/tags timed out")), timeoutMs);
  try {
    const response = await fetchFn(tagsUrl, {
      method: "GET",
      headers: { accept: "application/json", ...(provider.apiKey ? { authorization: `Bearer ${provider.apiKey}` } : {}) },
      signal: controller.signal,
    });
    if (!response.ok) return [];
    const payload = await readJsonLimited(response, 16 * 1024 * 1024);
    const root = record(payload);
    const items = Array.isArray(root.models) ? root.models : [];
    return items.map((item): AiModelSettings | null => {
      const raw = record(item);
      const id = text(raw.name).trim();
      if (!id) return null;
      return {
        id,
        label: id,
        task: "chat",
        inputModes: ["text"],
        outputModes: ["text"],
        supportedEfforts: [],
        defaultEffort: "auto",
        supportsTools: true,
      };
    }).filter((model): model is AiModelSettings => model !== null);
  } catch {
    return [];
  } finally {
    clearTimeout(timer);
  }
}

async function enrichOllamaModels(
  models: AiModelSettings[],
  origin: string,
  provider: AiProviderSettings,
  fetchFn: typeof fetch,
): Promise<void> {
  for (let index = 0; index < models.length; index += 1) {
    const model = models[index];
    if (!model) continue;
    try {
      const enriched = await fetchOllamaShow(origin, provider, model.id, fetchFn);
      if (!enriched) continue;
      models[index] = { ...model, ...enriched };
    } catch {
      // Best-effort enrich: keep the model without capability fields.
    }
  }
}

type ModelEnrich = {
  readonly supportsVision?: boolean;
  readonly supportsTools?: boolean;
  readonly contextWindow?: number;
};

async function fetchOllamaShow(
  origin: string,
  provider: AiProviderSettings,
  modelId: string,
  fetchFn: typeof fetch,
): Promise<ModelEnrich | null> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error("Ollama /api/show timed out")), 30_000);
  try {
    const response = await fetchFn(`${origin}/api/show`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        accept: "application/json",
        ...(provider.apiKey ? { authorization: `Bearer ${provider.apiKey}` } : {}),
      },
      body: JSON.stringify({ model: modelId }),
      signal: controller.signal,
    });
    if (!response.ok) return null;
    const payload = await readJsonLimited(response, 8 * 1024 * 1024);
    const root = record(payload);
    const capabilities = record(root.capabilities);
    const capabilitiesList = Array.isArray(root.capabilities) ? root.capabilities as unknown[] : [];
    const parameters = record(root.parameters);
    const modelInfo = record(root.model_info);
    const supportsVision = capabilitiesList.some((cap) => text(cap).toLowerCase() === "vision")
      || text(capabilities.vision).toLowerCase() === "true"
      || undefined;
    const supportsTools = capabilitiesList.some((cap) => text(cap).toLowerCase() === "tools")
      || text(capabilities.tools).toLowerCase() === "true"
      || undefined;
    const numCtx = positiveInteger(parameters.num_ctx)
      || findContextLength(modelInfo)
      || 0;
    return {
      ...(supportsVision !== undefined ? { supportsVision: supportsVision === true } : {}),
      ...(supportsTools !== undefined ? { supportsTools: supportsTools === true } : {}),
      ...(numCtx > 0 ? { contextWindow: numCtx } : {}),
    };
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

function findContextLength(modelInfo: Record<string, unknown>): number {
  for (const value of Object.values(modelInfo)) {
    if (typeof value === "number" && value > 0) continue;
    const str = text(value).toLowerCase();
    if (str.includes("context_length") || str.includes("context_length")) {
      const match = str.match(/(\d{3,})/);
      if (match) return positiveInteger(Number(match[1]));
    }
  }
  return 0;
}

async function enrichLlamaCppModels(
  models: AiModelSettings[],
  origin: string,
  provider: AiProviderSettings,
  fetchFn: typeof fetch,
): Promise<void> {
  // Try GET /props for vision + n_ctx (best-effort, shared across all models).
  let propsVision: boolean | undefined;
  let propsNctx: number | undefined;
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(new Error("llama.cpp /props timed out")), 30_000);
    let response: Response;
    try {
      response = await fetchFn(`${origin}/props`, {
        method: "GET",
        headers: { accept: "application/json", ...(provider.apiKey ? { authorization: `Bearer ${provider.apiKey}` } : {}) },
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timer);
    }
    if (response.ok) {
      const payload = await readJsonLimited(response, 8 * 1024 * 1024);
      const root = record(payload);
      const modalities = record(root.modalities);
      if (typeof modalities.vision === "boolean") propsVision = modalities.vision;
      else if (Array.isArray(root.modalities) && (root.modalities as unknown[]).some((m) => text(m).toLowerCase() === "vision")) propsVision = true;
      const defaultGen = record(root.default_generation_settings);
      propsNctx = positiveInteger(defaultGen.n_ctx) || undefined;
    }
  } catch {
    // Best-effort: skip props enrich.
  }

  for (let index = 0; index < models.length; index += 1) {
    const model = models[index];
    if (!model) continue;
    const contextWindow = model.contextWindow ?? propsNctx;
    const supportsVision = model.supportsVision ?? propsVision;
    models[index] = {
      ...model,
      ...(contextWindow ? { contextWindow } : {}),
      ...(supportsVision !== undefined ? { supportsVision } : {}),
      ...(model.supportsTools === undefined ? { supportsTools: true } : {}),
    };
  }
}

async function readJsonLimited(response: Response, maxBytes: number): Promise<unknown> {
  if (!response.body) throw new Error("Models endpoint returned an empty body");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let bytes = 0;
  let raw = "";
  while (true) {
    const chunk = await reader.read();
    if (chunk.done) break;
    bytes += chunk.value.byteLength;
    if (bytes > maxBytes) throw new Error("Models response exceeded the 16 MiB size limit");
    raw += decoder.decode(chunk.value, { stream: true });
  }
  raw += decoder.decode();
  try {
    return JSON.parse(raw);
  } catch {
    throw new Error("Models endpoint returned invalid JSON");
  }
}

function parseModelPage(payload: unknown): { items: readonly unknown[]; next: string } {
  if (Array.isArray(payload)) return { items: payload, next: "" };
  const root = record(payload);
  const items = Array.isArray(root.data) ? root.data : [];
  const links = record(root.links);
  const explicitNext = text(links.next);
  if (explicitNext) return { items, next: explicitNext };
  if (root.has_more === true && text(root.last_id)) {
    return { items, next: `?after_id=${encodeURIComponent(text(root.last_id))}` };
  }
  return { items, next: "" };
}

function normalizeImportedModel(value: unknown): AiModelSettings | null {
  const item = record(value);
  const id = text(item.id).trim();
  if (!id) return null;
  const architecture = record(item.architecture);
  const capabilities = record(item.capabilities);
  const reasoning = record(item.reasoning);
  const effortCapability = record(capabilities.effort);
  const inputModes = modes(architecture.input_modalities);
  const outputModes = modes(architecture.output_modalities);
  const shorthand = text(architecture.modality);
  if (inputModes.length === 0 && shorthand.includes("->")) {
    const [input, output] = shorthand.split("->", 2);
    inputModes.push(...modes(input?.split("+")));
    outputModes.push(...modes(output?.split("+")));
  }
  const imageInputSupported = record(capabilities.image_input).supported;
  if (imageInputSupported === true) addUnique(inputModes, "image");
  const supportsVision = typeof imageInputSupported === "boolean"
    ? imageInputSupported
    : inputModes.includes("image") ? true
      : inputModes.length > 0 ? false : undefined;
  if (record(capabilities.pdf_input).supported === true) addUnique(inputModes, "pdf");

  const supportedEfforts = normalizeEfforts(
    Array.isArray(reasoning.supported_efforts)
      ? reasoning.supported_efforts
      : effortCapability.supported === true
        ? ["low", "medium", "high", "max", "xhigh"].filter((level) => record(effortCapability[level]).supported === true)
        : [],
  );
  const supportedParameters = modes(item.supported_parameters);
  const supportsTools = supportedParameters.some((parameter) =>
    ["tools", "tool_choice", "parallel_tool_calls"].includes(parameter));
  const reasoningAdvertised = Object.keys(reasoning).length > 0
    || supportedParameters.some((parameter) => ["reasoning", "reasoning_effort", "include_reasoning"].includes(parameter))
    || effortCapability.supported === true;
  const defaultEffort = normalizeEffort(reasoning.default_effort);

  return {
    id,
    label: text(item.display_name) || text(item.name) || basenameLabel(id),
    task: normalizeTask(text(item.task), text(item.type)),
    ...(positiveInteger(item.context_length) || positiveInteger(item.max_input_tokens) || metaContextWindow(item.meta)
      ? { contextWindow: positiveInteger(item.context_length) || positiveInteger(item.max_input_tokens) || metaContextWindow(item.meta) } : {}),
    ...(positiveInteger(item.max_tokens) ? { maxOutput: positiveInteger(item.max_tokens) } : {}),
    inputModes,
    outputModes,
    supportedEfforts,
    defaultEffort: supportedEfforts.includes(defaultEffort)
      ? defaultEffort
      : reasoningAdvertised
        ? supportedEfforts[0] ?? "auto"
        : "auto",
    reasoningSupported: reasoningAdvertised,
    reasoningMandatory: reasoning.mandatory === true,
    reasoningSupportsMaxTokens: reasoning.supports_max_tokens === true,
    ...(supportsTools ? { supportsTools: true } : {}),
    ...(supportsVision !== undefined ? { supportsVision } : {}),
    ...(text(item.description) ? { description: text(item.description) } : {}),
  };
}

function normalizeProvider(value: unknown): AiProviderSettings | null {
  const provider = record(value);
  const id = text(provider.id).trim();
  if (!id || id === "stub" || provider.type === "stub") return null;
  const type: AiProviderType = inferProviderType({
    id,
    type: text(provider.type),
    baseUrl: text(provider.baseUrl),
  });
  const defaults = providerDefinition(type);
  const api: AiProviderApi = ["chat", "responses", "messages"].includes(text(provider.api))
    ? text(provider.api) as AiProviderApi : defaults.api;
  return {
    id,
    name: text(provider.name) || defaults.name || id,
    type,
    api,
    baseUrl: text(provider.baseUrl) || defaults.baseUrl,
    ...(text(provider.apiKey) ? { apiKey: text(provider.apiKey) } : {}),
    apiKeyOptional: defaults.apiKeyOptional,
    enabled: provider.enabled !== false,
    defaultModel: text(provider.defaultModel),
    timeoutMs: integerInRange(provider.timeoutMs, 1000, 600_000, 60_000),
    maxAttempts: integerInRange(provider.maxAttempts, 1, 10, 1),
    weight: integerInRange(provider.weight, 1, 100, 1),
    models: Array.isArray(provider.models)
      ? provider.models.map(normalizeModel).filter((model): model is AiModelSettings => model !== null)
      : [],
  };
}

function normalizeModel(value: unknown): AiModelSettings | null {
  const model = record(value);
  const id = text(model.id).trim();
  if (!id) return null;
  const supportedEfforts = normalizeEfforts(model.supportedEfforts);
  const defaultEffort = normalizeEffort(model.defaultEffort);
  return {
    id,
    label: text(model.label) || id,
    task: text(model.task),
    ...(positiveInteger(model.contextWindow) ? { contextWindow: positiveInteger(model.contextWindow) } : {}),
    ...(positiveInteger(model.maxOutput) ? { maxOutput: positiveInteger(model.maxOutput) } : {}),
    inputModes: modes(model.inputModes),
    outputModes: modes(model.outputModes),
    supportedEfforts,
    defaultEffort: supportedEfforts.includes(defaultEffort) ? defaultEffort : supportedEfforts[0] ?? "auto",
    ...(typeof model.reasoningSupported === "boolean" ? { reasoningSupported: model.reasoningSupported } : {}),
    ...(model.reasoningMandatory === true ? { reasoningMandatory: true } : {}),
    ...(model.reasoningSupportsMaxTokens === true ? { reasoningSupportsMaxTokens: true } : {}),
    ...(typeof model.supportsTools === "boolean" ? { supportsTools: model.supportsTools } : {}),
    ...(typeof model.supportsVision === "boolean" ? { supportsVision: model.supportsVision } : {}),
    ...(text(model.description) ? { description: text(model.description) } : {}),
  };
}

function isChatSelectable(model: AiModelSettings): boolean {
  const task = model.task.trim().toLowerCase();
  if (nonChatTasks.has(task)) return false;
  if (model.outputModes.length > 0 && !model.outputModes.includes("text")) return false;
  const id = model.id.toLowerCase();
  return !nonChatMarkers.some((marker) => id.includes(marker));
}

function basicModel(id: string, label = id): AiModelSettings {
  return {
    id,
    label,
    task: "chat",
    inputModes: ["text"],
    outputModes: ["text"],
    supportedEfforts: [],
    defaultEffort: "auto",
    supportsTools: true,
  };
}

function normalizeTask(task: string, type: string): string {
  const normalized = task.trim().toLowerCase();
  if (normalized) return normalized;
  const fallback = type.trim().toLowerCase();
  return fallback === "model" ? "" : fallback;
}

function normalizeEfforts(value: unknown): ReasoningEffort[] {
  if (!Array.isArray(value)) return [];
  return [...new Set(value.map(normalizeEffort).filter((level) => level !== "auto"))];
}

function normalizeEffort(value: unknown): ReasoningEffort {
  const aliases: Record<string, ReasoningEffort> = {
    off: "none",
    min: "minimal",
    med: "medium",
    "x-high": "xhigh",
    extra: "xhigh",
    maximum: "max",
    // UI says "default"; catalogs sometimes use the same word.
    default: "auto",
  };
  const raw = text(value).toLowerCase();
  const normalized = (aliases[raw] ?? raw) as ReasoningEffort;
  return efforts.includes(normalized) ? normalized : "auto";
}

function normalizeStrategy(value: unknown): AiRegistrySettings["strategy"] {
  return value === "round-robin" || value === "switch" ? value : "failover";
}

function normalizeVision(value: unknown): AiRegistrySettings["vision"] {
  return value === "on" || value === "off" ? value : "auto";
}

function integerInRange(value: unknown, min: number, max: number, fallback: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= min && value <= max
    ? value
    : fallback;
}

function modes(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return [...new Set(value.map((item) => text(item).trim().toLowerCase()).filter(Boolean))];
}

function addUnique(values: string[], value: string): void {
  if (!values.includes(value)) values.push(value);
}

function positiveInteger(value: unknown): number {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : 0;
}

function basenameLabel(id: string): string {
  if (!id.includes("/") && !id.includes("\\")) return id;
  const parts = id.replace(/\\/g, "/").split("/").filter(Boolean);
  return parts[parts.length - 1] || id;
}

function metaContextWindow(meta: unknown): number {
  const record = typeof meta === "object" && meta !== null && !Array.isArray(meta) ? meta as Record<string, unknown> : {};
  return positiveInteger(record.n_ctx) || positiveInteger(record.n_ctx_train) || positiveInteger(record.context_length) || 0;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown> : {};
}
