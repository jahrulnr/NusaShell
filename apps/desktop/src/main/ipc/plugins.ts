import { ipcMain } from "electron";
import { randomUUID } from "node:crypto";
import type { AcpConfigOption, CallToolCommand, ImportAcpModelsCommand, ImportAcpModelsResult, ListToolsQuery } from "@nusashell/application";
import type { IpcContext } from "./ipc-context.js";
import type {
  AcpConfigOptionSummary,
  AcpModelOption,
  AcpProviderSaveInput,
  AcpRoutingSettings,
} from "../../shared/acp-provider-contract.js";
import { probeAcpProviderAuth } from "../acp-auth.js";

/** Register plugin tool + ACP provider IPC handlers. */
export function registerPluginsIpc(ctx: IpcContext): void {
  ipcMain.handle("tool:call", async (_event, pluginId: string, toolName: string, args: Record<string, unknown>) => {
    const command: CallToolCommand = {
      kind: "call-tool",
      pluginId,
      requestId: randomUUID(),
      toolName,
      args: args ?? {},
    };
    ctx.logTail.add("ipc", "info", `tool.call ${pluginId}.${toolName} (${command.requestId})`);
    try {
      const result = await ctx.commandBus.execute(command);
      ctx.logTail.add("ipc", "info", `tool.call completed ${pluginId}.${toolName} (${command.requestId})`);
      return result;
    } catch (error) {
      ctx.logTail.add("ipc", "error", `tool.call failed ${pluginId}.${toolName}: ${String(error)}`);
      throw error;
    }
  });

  ipcMain.handle("tool:list", async (_event, pluginId: string) => {
    const query: ListToolsQuery = { kind: "list-tools", pluginId };
    ctx.logTail.add("ipc", "debug", `tool.list ${pluginId}`);
    return ctx.queryBus.execute(query);
  });

  ipcMain.handle("acp-providers:list", () => ctx.getAcpProviderStore().list());
  ipcMain.handle("acp-providers:save", (_event, input: AcpProviderSaveInput) =>
    ctx.getAcpProviderStore().save(input));
  ipcMain.handle("acp-providers:get", (_event, providerId: string) =>
    ctx.getAcpProviderStore().getEffective(providerId));
  ipcMain.handle("acp-providers:probe", async (_event, providerId: string, options?: { interactive?: boolean }) => {
    return probeAcpProviderAuth(
      ctx.getAcpProviderStore(),
      ctx.commandBus,
      providerId,
      { interactive: options?.interactive !== false },
    );
  });
  ipcMain.handle("acp-providers:get-routing", () => ctx.getAcpProviderStore().getRouting());
  ipcMain.handle("acp-providers:save-routing", (_event, settings: AcpRoutingSettings) =>
    ctx.getAcpProviderStore().saveRouting(settings));
  ipcMain.handle("acp-providers:import-models", async (_event, providerId: string) => {
    return importAcpModels(ctx, providerId);
  });
  ipcMain.handle("acp-providers:set-default-model", async (_event, providerId: string, modelId: string) => {
    return ctx.getAcpProviderStore().setDefaultModel(providerId, modelId);
  });
  ipcMain.handle("acp-providers:set-default-mode", async (_event, providerId: string, mode: string) => {
    return ctx.getAcpProviderStore().setDefaultMode(providerId, mode);
  });
}

/**
 * Probe a fresh session for a provider, extract the model list + config-option
 * snapshot, persist them via the store, and return the discovered models.
 * Mirrors the probe IPC pattern (command bus, not WS from renderer).
 */
async function importAcpModels(ctx: IpcContext, providerId: string): Promise<{ models: AcpModelOption[]; error?: string }> {
  const store = ctx.getAcpProviderStore();
  const provider = await store.getEffective(providerId);
  if (!provider) throw new Error(`ACP provider not found: ${providerId}`);
  const command: ImportAcpModelsCommand = {
    kind: "import-acp-models",
    provider: {
      providerId: provider.manifest.id,
      command: provider.config.command || provider.manifest.command,
      args: provider.config.args ?? provider.manifest.args,
      ...(provider.config.authMethodId || provider.manifest.authMethodId
        ? { authMethodId: provider.config.authMethodId || provider.manifest.authMethodId }
        : {}),
      ...(provider.manifest.env ? { env: provider.manifest.env } : {}),
      ...((provider.config.preferredConfig ?? provider.manifest.preferredConfig)
        ? { preferredConfig: provider.config.preferredConfig ?? provider.manifest.preferredConfig }
        : {}),
    },
  };
  const result = (await ctx.commandBus.execute(command)) as ImportAcpModelsResult;
  if (!result.ok) return { models: [], error: result.error };
  const models = extractModels(result.configOptions);
  const configOptions = toConfigOptionSummaries(result.configOptions);
  const existingDefault = provider.config.defaultModelId;
  const modelOption = result.configOptions.find((o: AcpConfigOption) => o.id === "model");
  const defaultModelId = existingDefault
    ?? (typeof modelOption?.currentValue === "string" && modelOption.currentValue ? modelOption.currentValue : undefined)
    ?? models[0]?.id;
  await store.save({
    providerId,
    models,
    configOptions,
    ...(defaultModelId ? { defaultModelId } : {}),
  });
  return { models };
}

function extractModels(options: readonly AcpConfigOption[]): AcpModelOption[] {
  const modelOption = options.find((o) => o.id === "model");
  if (!modelOption?.options) return [];
  return modelOption.options.map((opt) => ({
    id: opt.value,
    label: opt.name,
    ...(opt.description ? { description: opt.description } : {}),
  }));
}

function toConfigOptionSummaries(options: readonly AcpConfigOption[]): AcpConfigOptionSummary[] {
  return options.map((o) => ({
    id: o.id,
    name: o.name,
    type: o.type,
    currentValue: o.currentValue,
    ...(o.options ? { options: o.options.map((opt) => ({ value: opt.value, name: opt.name, ...(opt.description ? { description: opt.description } : {}) })) } : {}),
    ...(o.description ? { description: o.description } : {}),
    ...(o.category ? { category: o.category } : {}),
  }));
}
