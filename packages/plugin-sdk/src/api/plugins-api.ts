import type { ParsedEvent, EventType, PluginListItem, PluginStateResultDto, PluginGetResultDto, PluginInstallResult, PluginUninstallResult, ToolCallResultDto, ToolListResultDto } from "@nusashell/contracts";
import { NusaClient } from "../client/nusa-client.js";

export class PluginsApi {
  constructor(private readonly client: NusaClient) {}

  start(pluginId: string, timeoutMs?: number): Promise<PluginStateResultDto> {
    return this.client.request("plugin.start", { pluginId }, timeoutMs);
  }

  stop(pluginId: string, timeoutMs?: number): Promise<PluginStateResultDto> {
    return this.client.request("plugin.stop", { pluginId }, timeoutMs);
  }

  list(timeoutMs?: number): Promise<{ plugins: readonly PluginListItem[] }> {
    return this.client.request("plugin.list", {}, timeoutMs);
  }

  restart(pluginId: string, timeoutMs?: number): Promise<PluginStateResultDto> {
    return this.client.request("plugin.restart", { pluginId }, timeoutMs);
  }

  install(source: "url" | "local", path: string, timeoutMs?: number): Promise<PluginInstallResult> {
    return this.client.request("plugin.install", { source, path }, timeoutMs);
  }

  uninstall(pluginId: string, timeoutMs?: number): Promise<PluginUninstallResult> {
    return this.client.request("plugin.uninstall", { pluginId }, timeoutMs);
  }

  get(pluginId: string, timeoutMs?: number): Promise<PluginGetResultDto> {
    return this.client.request("plugin.get", { pluginId }, timeoutMs);
  }

  getState(pluginId: string, timeoutMs?: number): Promise<PluginStateResultDto> {
    return this.client.request("plugin.state", { pluginId }, timeoutMs);
  }
}

export class ToolsApi {
  constructor(private readonly client: NusaClient) {}

  call(
    pluginId: string,
    requestId: string,
    toolName: string,
    args: Record<string, unknown>,
    timeoutMs?: number,
  ): Promise<ToolCallResultDto> {
    return this.client.request(
      "tool.call",
      { pluginId, requestId, toolName, args, ...(timeoutMs !== undefined ? { timeoutMs } : {}) },
      timeoutMs,
    );
  }

  cancel(pluginId: string, requestId: string): Promise<unknown> {
    return this.client.request("tool.cancel", { pluginId, requestId });
  }

  list(pluginId: string, timeoutMs?: number): Promise<ToolListResultDto> {
    return this.client.request("tool.list", { pluginId }, timeoutMs);
  }
}

export type { ParsedEvent, EventType };
