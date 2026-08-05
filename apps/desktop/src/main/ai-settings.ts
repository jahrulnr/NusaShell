import { safeStorage } from "electron";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import {
  flattenModelCatalog,
  importProviderModels,
  normalizeRegistryState,
  type AiModelSettings,
  type AiProviderSettings,
  type AiRegistrySettings,
} from "./ai-provider-registry.js";
import type {
  PublicAiRegistry,
  ReasoningEffort,
  SaveAiProviderInput,
} from "../shared/ai-contract.js";
import { normalizeProviderInput } from "./ai-provider-definitions.js";

export class AiSettingsStore {
  private state: AiRegistrySettings | null = null;

  constructor(
    private readonly path: string,
    private readonly userPromptPath: string,
  ) {}

  async load(): Promise<AiRegistrySettings> {
    if (this.state) return this.state;
    try {
      const raw = JSON.parse(await readFile(this.path, "utf8")) as Record<string, unknown>;
      const userPrompt = await this.loadUserPrompt();
      this.state = normalizeRegistryState({ ...decryptStoredKeys(raw), userPrompt });
    } catch (error) {
      if (isFileNotFound(error)) {
        const userPrompt = await this.loadUserPrompt().catch(() => "");
        this.state = normalizeRegistryState({ userPrompt });
      } else {
        throw new Error("Could not load AI settings", { cause: error });
      }
    }
    return this.state;
  }

  async getPublic(): Promise<PublicAiRegistry> {
    return this.public(await this.load());
  }

  async saveProvider(input: SaveAiProviderInput): Promise<PublicAiRegistry> {
    input = normalizeProviderInput(input);
    const current = await this.load();
    const id = normalizeProviderId(input.id);
    const existing = current.providers.find((provider) => provider.id === id);
    const apiKey = input.apiKey?.trim() || existing?.apiKey;
    const baseUrl = input.baseUrl.trim().replace(/\/+$/, "");
    const name = input.name.trim();
    if (!id || !name) throw new Error("Provider name and ID are required");
    if (!baseUrl) throw new Error("Provider base URL is required");
    if (!input.apiKeyOptional && !apiKey) throw new Error("Provider API key is required");
    if (apiKey && !safeStorage.isEncryptionAvailable()) {
      throw new Error("Secure credential storage is unavailable on this system");
    }

    const provider: AiProviderSettings = {
      id,
      name,
      type: input.type,
      api: input.api,
      baseUrl,
      ...(apiKey ? { apiKey } : {}),
      apiKeyOptional: input.apiKeyOptional,
      enabled: input.enabled,
      defaultModel: input.defaultModel?.trim() ?? existing?.defaultModel ?? "",
      timeoutMs: integerInRange(input.timeoutMs, 1000, 600_000, existing?.timeoutMs ?? defaultTimeoutForType(input.type)),
      maxAttempts: integerInRange(input.maxAttempts, 1, 10, existing?.maxAttempts ?? 1),
      weight: integerInRange(input.weight, 1, 100, existing?.weight ?? 1),
      models: existing?.models ?? [],
    };
    const providers = existing
      ? current.providers.map((item) => item.id === id ? provider : item)
      : [provider, ...current.providers];
    const disabledActiveProvider = !provider.enabled && current.activeProviderId === provider.id;
    const disabledActiveModel = !provider.enabled && current.activeModelKey.startsWith(`${provider.id}::`);
    this.state = {
      ...current,
      activeProviderId: provider.enabled
        ? provider.id
        : disabledActiveProvider
          ? providers.find((item) => item.enabled)?.id ?? ""
          : current.activeProviderId,
      activeModelKey: disabledActiveModel ? "" : current.activeModelKey,
      effort: disabledActiveModel ? "auto" : current.effort,
      providers,
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  async deleteProvider(providerId: string): Promise<PublicAiRegistry> {
    const current = await this.load();
    const id = normalizeProviderId(providerId);
    if (!current.providers.some((provider) => provider.id === id)) {
      return this.public(current);
    }

    const providers = current.providers.filter((provider) => provider.id !== id);
    const deletedActiveModel = current.activeModelKey.startsWith(`${id}::`);
    this.state = {
      ...current,
      activeProviderId: current.activeProviderId === id
        ? providers.find((provider) => provider.enabled)?.id ?? ""
        : current.activeProviderId,
      activeModelKey: deletedActiveModel ? "" : current.activeModelKey,
      effort: deletedActiveModel ? "auto" : current.effort,
      providers,
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  async importModels(providerId: string, fetchFn: typeof fetch = fetch): Promise<PublicAiRegistry> {
    const current = await this.load();
    const provider = current.providers.find((item) => item.id === providerId);
    if (!provider) throw new Error(`Provider not found: ${providerId}`);
    const models = await importProviderModels(provider, fetchFn);
    const updated: AiProviderSettings = {
      ...provider,
      models,
      defaultModel: provider.defaultModel && models.some((model) => model.id === provider.defaultModel)
        ? provider.defaultModel : "",
    };
    const modelKeys = new Set(models.map((model) => `${provider.id}::${model.id}`));
    this.state = {
      ...current,
      activeModelKey: current.activeProviderId === provider.id && !modelKeys.has(current.activeModelKey)
        ? "" : current.activeModelKey,
      providers: current.providers.map((item) => item.id === provider.id ? updated : item),
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  async addModel(providerId: string, model: Pick<AiModelSettings, "id" | "label"> & Partial<Pick<AiModelSettings, "contextWindow">>): Promise<PublicAiRegistry> {
    const current = await this.load();
    const provider = current.providers.find((item) => item.id === providerId);
    if (!provider) throw new Error(`Provider not found: ${providerId}`);
    const id = model.id.trim();
    if (!id) throw new Error("Model ID is required");
    const next: AiModelSettings = {
      id,
      label: model.label.trim() || id,
      task: "chat",
      ...(typeof model.contextWindow === "number" && model.contextWindow > 0 ? { contextWindow: model.contextWindow } : {}),
      inputModes: ["text"],
      outputModes: ["text"],
      supportedEfforts: [],
      defaultEffort: "auto",
      supportsTools: true,
    };
    const models = provider.models.some((item) => item.id === id)
      ? provider.models.map((item) => item.id === id ? next : item)
      : [...provider.models, next];
    this.state = {
      ...current,
      providers: current.providers.map((item) => item.id === provider.id ? { ...provider, models } : item),
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  async select(input: { modelKey?: string; effort?: ReasoningEffort }): Promise<PublicAiRegistry> {
    const current = await this.load();
    const models = flattenModelCatalog(current.providers);
    const selected = input.modelKey ? models.find((model) => model.key === input.modelKey) : undefined;
    if (input.modelKey && !selected) throw new Error("Selected model is unavailable");
    const wantedEffort = input.effort ?? current.effort;
    const effort = selected && wantedEffort !== "auto"
      ? selected.supportedEfforts.length === 0
        ? "auto"
        : selected.supportedEfforts.includes(wantedEffort)
          ? wantedEffort
          : selected.defaultEffort
      : wantedEffort;
    this.state = {
      ...current,
      activeProviderId: selected?.providerId ?? current.activeProviderId,
      activeModelKey: input.modelKey ?? current.activeModelKey,
      effort,
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  async updateRuntime(input: {
    readonly strategy?: AiRegistrySettings["strategy"];
    readonly totalAttemptBudget?: number;
    readonly stream?: boolean;
    readonly vision?: AiRegistrySettings["vision"];
    readonly userPrompt?: string;
    readonly maxToolRounds?: number;
    readonly maxRepeatedToolCalls?: number;
    readonly maxAutoContinues?: number;
    readonly compactionEnabled?: boolean;
    readonly maxInputTokens?: number;
    readonly reserveTokens?: number;
    readonly recentTurns?: number;
    readonly summaryMaxChars?: number;
  }): Promise<PublicAiRegistry> {
    const current = await this.load();
    const userPrompt = typeof input.userPrompt === "string" ? input.userPrompt.trim() : current.userPrompt;
    await this.persistUserPrompt(userPrompt);
    this.state = {
      ...current,
      strategy: input.strategy === "round-robin" || input.strategy === "switch" ? input.strategy : "failover",
      totalAttemptBudget: integerInRange(input.totalAttemptBudget, 1, 32, current.totalAttemptBudget),
      stream: input.stream !== false,
      vision: input.vision === "on" || input.vision === "off" ? input.vision : "auto",
      userPrompt,
      maxToolRounds: integerInRange(input.maxToolRounds, 0, 10_000, current.maxToolRounds),
      maxRepeatedToolCalls: integerInRange(input.maxRepeatedToolCalls, 1, 200, current.maxRepeatedToolCalls),
      maxAutoContinues: integerInRange(input.maxAutoContinues, 0, 10_000, current.maxAutoContinues),
      compactionEnabled: typeof input.compactionEnabled === "boolean" ? input.compactionEnabled : current.compactionEnabled,
      maxInputTokens: integerInRange(input.maxInputTokens, 1000, 2_000_000, current.maxInputTokens),
      reserveTokens: integerInRange(input.reserveTokens, 0, 1_000_000, current.reserveTokens),
      recentTurns: integerInRange(input.recentTurns, 1, 100, current.recentTurns),
      summaryMaxChars: integerInRange(input.summaryMaxChars, 100, 1_000_000, current.summaryMaxChars),
    };
    await this.persist(this.state);
    return this.public(this.state);
  }

  public(settings: AiRegistrySettings): PublicAiRegistry {
    return {
      activeProviderId: settings.activeProviderId,
      activeModelKey: settings.activeModelKey,
      effort: settings.effort,
      strategy: settings.strategy,
      totalAttemptBudget: settings.totalAttemptBudget,
      stream: settings.stream,
      vision: settings.vision,
      userPrompt: settings.userPrompt,
      maxToolRounds: settings.maxToolRounds,
      maxRepeatedToolCalls: settings.maxRepeatedToolCalls,
      maxAutoContinues: settings.maxAutoContinues,
      compactionEnabled: settings.compactionEnabled,
      maxInputTokens: settings.maxInputTokens,
      reserveTokens: settings.reserveTokens,
      recentTurns: settings.recentTurns,
      summaryMaxChars: settings.summaryMaxChars,
      canPersistApiKey: safeStorage.isEncryptionAvailable(),
      providers: settings.providers.map(({ apiKey, ...provider }) => ({
        ...provider,
        hasApiKey: Boolean(apiKey),
      })),
      models: flattenModelCatalog(settings.providers),
    };
  }

  private async persist(settings: AiRegistrySettings): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const providers = settings.providers.map((provider) => ({
      ...provider,
      ...(provider.apiKey ? { apiKey: safeStorage.encryptString(provider.apiKey).toString("base64") } : {}),
    }));
    const { userPrompt: _userPrompt, ...settingsToPersist } = settings;
    const temporaryPath = `${this.path}.tmp`;
    await writeFile(temporaryPath, JSON.stringify({ ...settingsToPersist, providers }, null, 2), { mode: 0o600 });
    await rename(temporaryPath, this.path);
  }

  private async loadUserPrompt(): Promise<string> {
    try {
      return (await readFile(this.userPromptPath, "utf8")).trim();
    } catch (error) {
      if (isFileNotFound(error)) {
        return "";
      }
      throw new Error("Could not load user prompt", { cause: error });
    }
  }

  private async persistUserPrompt(userPrompt: string): Promise<void> {
    await mkdir(dirname(this.userPromptPath), { recursive: true });
    const temporaryPath = `${this.userPromptPath}.tmp`;
    await writeFile(temporaryPath, userPrompt, { mode: 0o600 });
    await rename(temporaryPath, this.userPromptPath);
  }
}

function decryptStoredKeys(raw: Record<string, unknown>): Record<string, unknown> {
  if (!safeStorage.isEncryptionAvailable()) {
    if (Array.isArray(raw.providers)) {
      return {
        ...raw,
        providers: raw.providers.map((value) => {
          const { apiKey: _encryptedKey, ...provider } = record(value);
          return provider;
        }),
      };
    }
    const { apiKey: _encryptedKey, ...withoutKey } = raw;
    return withoutKey;
  }
  if (Array.isArray(raw.providers)) {
    return {
      ...raw,
      providers: raw.providers.map((value) => {
        const provider = record(value);
        const encrypted = text(provider.apiKey);
        return {
          ...provider,
          ...(encrypted ? { apiKey: decrypt(encrypted) } : {}),
        };
      }),
    };
  }
  const encrypted = text(raw.apiKey);
  return { ...raw, ...(encrypted ? { apiKey: decrypt(encrypted) } : {}) };
}

function decrypt(value: string): string {
  try {
    return safeStorage.decryptString(Buffer.from(value, "base64"));
  } catch {
    return "";
  }
}

function normalizeProviderId(value: string): string {
  return value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown> : {};
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}

function integerInRange(value: number | undefined, min: number, max: number, fallback: number): number {
  return Number.isInteger(value) && (value ?? 0) >= min && (value ?? 0) <= max
    ? value as number
    : fallback;
}

function defaultTimeoutForType(type: string): number {
  return type === "ollama" || type === "llamacpp" ? 180_000 : 60_000;
}

export type {
  AgentModelOption,
  AiProviderSettings,
  AiRegistrySettings,
  PublicAiRegistry,
  ReasoningEffort,
  SaveAiProviderInput,
} from "../shared/ai-contract.js";
