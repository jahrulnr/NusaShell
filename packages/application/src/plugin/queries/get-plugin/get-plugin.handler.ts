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
    return view;
  }
}
