import type {
  AiProviderApi,
  AiProviderType,
  SaveAiProviderInput,
} from "../shared/ai-contract.js";

export interface AiProviderDefinition {
  readonly type: AiProviderType;
  readonly name: string;
  readonly description: string;
  readonly api: AiProviderApi;
  readonly baseUrl: string;
  readonly apiKeyOptional: boolean;
  readonly hideFromCatalog?: boolean;
  readonly ids: readonly string[];
  readonly hosts: readonly string[];
}

export const aiProviderDefinitions: readonly AiProviderDefinition[] = [
  definition("openrouter", "OpenRouter", "One API for models from many upstream providers.", "chat", "https://openrouter.ai/api/v1", false, ["openrouter"], ["openrouter.ai"]),
  definition("omniroute", "OmniRoute", "Local multi-provider AI gateway with OpenAI-compatible endpoints.", "responses", "http://127.0.0.1:20128/v1", true, ["omniroute"], [":20128"]),
  definition("9router", "9Router", "Local 9Router gateway using its OpenAI-compatible endpoint.", "chat", "http://127.0.0.1:20128/v1", true, ["9router", "9_router"], [":20130"]),
  definition("openai", "OpenAI", "Official OpenAI API using the Responses API by default.", "responses", "https://api.openai.com/v1", false, ["openai"], ["api.openai.com"]),
  definition("claude", "Claude API", "Official Anthropic Messages API for Claude models.", "messages", "https://api.anthropic.com/v1", false, ["claude", "anthropic"], ["api.anthropic.com"]),
  definition("ollama", "Ollama", "Local Ollama OpenAI-compatible API.", "chat", "http://127.0.0.1:11434/v1", true, ["ollama"], [":11434"]),
  definition("llamacpp", "llama.cpp", "Local llama-server OpenAI-compatible API.", "chat", "http://127.0.0.1:8080/v1", true, ["llamacpp", "llama.cpp", "llama-cpp"], ["localhost:8080", "127.0.0.1:8080"]),
  {
    ...definition("openai-compatible", "OpenAI Compatible", "Custom OpenAI-compatible endpoint.", "chat", "", false, [], []),
    hideFromCatalog: true,
  },
];

const byType = new Map(aiProviderDefinitions.map((item) => [item.type, item]));

export function inferProviderType(value: {
  readonly id?: string;
  readonly type?: string;
  readonly baseUrl?: string;
}): AiProviderType {
  const explicit = normalizedType(value.type);
  if (explicit) return explicit;
  const id = value.id?.trim().toLowerCase() ?? "";
  const baseUrl = value.baseUrl?.trim().toLowerCase() ?? "";
  return aiProviderDefinitions.find((item) =>
    item.type !== "openai-compatible"
    && (item.ids.includes(id) || item.hosts.some((host) => baseUrl.includes(host))),
  )?.type ?? "openai-compatible";
}

export function normalizeProviderInput(input: SaveAiProviderInput): SaveAiProviderInput {
  const type = inferProviderType(input);
  const defaults = byType.get(type)!;
  return {
    ...input,
    type,
    name: input.name.trim() || defaults.name,
    api: input.api || defaults.api,
    baseUrl: input.baseUrl.trim().replace(/\/+$/, "") || defaults.baseUrl,
    apiKeyOptional: defaults.apiKeyOptional,
  };
}

export function providerDefinition(type: AiProviderType): AiProviderDefinition {
  return byType.get(type) ?? byType.get("openai-compatible")!;
}

function normalizedType(value: unknown): AiProviderType | undefined {
  return typeof value === "string" && byType.has(value.toLowerCase() as AiProviderType)
    ? value.toLowerCase() as AiProviderType
    : undefined;
}

function definition(
  type: AiProviderType,
  name: string,
  description: string,
  api: AiProviderApi,
  baseUrl: string,
  apiKeyOptional: boolean,
  ids: readonly string[],
  hosts: readonly string[],
): AiProviderDefinition {
  return { type, name, description, api, baseUrl, apiKeyOptional, ids, hosts };
}
