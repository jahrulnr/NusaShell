import type { ParsedEvent, EventType, PluginListItem, PluginStateResultDto, ToolCallResultDto } from "@nusashell/contracts";
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
}

export type { ParsedEvent, EventType };
