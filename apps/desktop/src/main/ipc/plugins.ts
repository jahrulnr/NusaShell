import { ipcMain } from "electron";
import { randomUUID } from "node:crypto";
import type { CallToolCommand, ListToolsQuery } from "@nusashell/application";
import type { IpcContext } from "./ipc-context.js";
import type { AcpProviderSaveInput, AcpRoutingSettings } from "../../shared/acp-provider-contract.js";
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
}
