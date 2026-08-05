import { ipcMain } from "electron";
import type { IpcContext } from "./ipc-context.js";
import type { AiRegistrySettings, SaveAiProviderInput } from "../ai-settings.js";
import type { ConfigureAiCommand, ConfigureAiRuntimeCommand, RemoveAiCommand } from "@nusashell/application";

function normalizeProviderId(value: string): string {
  return value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
}

/** Register AI provider + runtime IPC handlers. */
export function registerAiIpc(ctx: IpcContext): void {
  ipcMain.handle("ai-providers:list", () => ctx.getAiSettingsStore().getPublic());

  ipcMain.handle("ai-providers:save", async (_event, input: SaveAiProviderInput) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.saveProvider(input);
    const savedId = normalizeProviderId(input.id);
    const removeCmd: RemoveAiCommand = { kind: "remove-ai", providerId: savedId };
    ctx.commandBus.execute(removeCmd);
    const aiSettings = await store.load();
    const provider = aiSettings.providers.find((item) => item.id === savedId);
    if (provider) configureProvider(ctx, provider);
    return result;
  });

  ipcMain.handle("ai-providers:delete", async (_event, providerId: string) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.deleteProvider(providerId);
    const removeCmd: RemoveAiCommand = { kind: "remove-ai", providerId };
    ctx.commandBus.execute(removeCmd);
    await store.load();
    return result;
  });

  ipcMain.handle("ai-providers:import-models", async (_event, providerId: string) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.importModels(providerId);
    await store.load();
    return result;
  });

  ipcMain.handle("ai-providers:add-model", async (_event, providerId: string, model: { id: string; label: string; contextWindow?: number }) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.addModel(providerId, model);
    await store.load();
    return result;
  });

  ipcMain.handle("ai-providers:select", async (_event, input: { modelKey?: string; effort?: AiRegistrySettings["effort"] }) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.select(input);
    await store.load();
    return result;
  });

  ipcMain.handle("ai-providers:update-runtime", async (_event, input: Pick<AiRegistrySettings, "strategy" | "totalAttemptBudget" | "stream" | "vision" | "userPrompt" | "maxToolRounds" | "maxRepeatedToolCalls" | "maxAutoContinues" | "compactionEnabled" | "maxInputTokens" | "reserveTokens" | "recentTurns" | "summaryMaxChars">) => {
    const store = ctx.getAiSettingsStore();
    const result = await store.updateRuntime(input);
    const aiSettings = await store.load();
    const runtimeCmd: ConfigureAiRuntimeCommand = {
      kind: "configure-ai-runtime",
      strategy: aiSettings.strategy,
      totalAttemptBudget: aiSettings.totalAttemptBudget,
      stream: aiSettings.stream,
      vision: aiSettings.vision,
      userPrompt: aiSettings.userPrompt,
      maxToolRounds: aiSettings.maxToolRounds,
      maxRepeatedToolCalls: aiSettings.maxRepeatedToolCalls,
      maxAutoContinues: aiSettings.maxAutoContinues,
      compactionEnabled: aiSettings.compactionEnabled,
      maxInputTokens: aiSettings.maxInputTokens,
      reserveTokens: aiSettings.reserveTokens,
      recentTurns: aiSettings.recentTurns,
      summaryMaxChars: aiSettings.summaryMaxChars,
    };
    ctx.commandBus.execute(runtimeCmd);
    for (const provider of aiSettings.providers) {
      const removeCmd: RemoveAiCommand = { kind: "remove-ai", providerId: provider.id };
      ctx.commandBus.execute(removeCmd);
      configureProvider(ctx, provider);
    }
    return result;
  });
}

function configureProvider(ctx: IpcContext, provider: AiRegistrySettings["providers"][number]): void {
  if (!provider.enabled || !provider.baseUrl) return;
  if (!provider.apiKeyOptional && !provider.apiKey) return;
  const cmd: ConfigureAiCommand = {
    kind: "configure-ai",
    providerId: provider.id,
    api: provider.api,
    baseUrl: provider.baseUrl,
    ...(provider.apiKey ? { apiKey: provider.apiKey } : {}),
    ...(provider.defaultModel ? { model: provider.defaultModel } : {}),
    timeoutMs: provider.timeoutMs,
    maxAttempts: provider.maxAttempts,
    omitToolChoice: provider.type === "ollama" || provider.type === "llamacpp",
  };
  ctx.commandBus.execute(cmd);
}
