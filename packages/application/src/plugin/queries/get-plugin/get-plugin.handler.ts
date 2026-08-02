import { PluginId } from "@nusashell/domain";
import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { GetPluginQuery } from "./get-plugin.query.js";
import type { GetPluginResult } from "./get-plugin.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class GetPluginHandler
  implements QueryHandler<GetPluginQuery, GetPluginResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(query: GetPluginQuery): Promise<GetPluginResult> {
    const idResult = PluginId.create(query.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const view = await this.runtimeManager.getPlugin(idResult.value);
    if (!view) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Plugin not found: ${query.pluginId}`,
        { pluginId: query.pluginId },
      );
    }
    return {
      pluginId: view.pluginId,
      name: view.name,
      version: view.version,
      icon: view.icon,
      installPath: view.installPath,
      state: view.state,
      enabled: view.enabled,
      autostart: view.autostart,
      ...(view.source !== undefined ? { source: view.source } : {}),
      ...(view.transport !== undefined ? { transport: view.transport } : {}),
      ...(view.category !== undefined ? { category: view.category } : {}),
      ...(view.command !== undefined ? { command: view.command } : {}),
      ...(view.args !== undefined ? { args: view.args } : {}),
      ...(view.url !== undefined ? { url: view.url } : {}),
      ...(view.env !== undefined ? { env: view.env } : {}),
      ...(view.headers !== undefined ? { headers: view.headers } : {}),
      ...(view.ui !== undefined ? { ui: view.ui } : {}),
      keepAliveOnClose: view.keepAliveOnClose,
    };
  }
}
