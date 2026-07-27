import { PluginId } from "@nusashell/domain";
import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { GetPluginStateQuery } from "./get-plugin-state.query.js";
import type { GetPluginStateResult } from "./get-plugin-state.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class GetPluginStateHandler
  implements QueryHandler<GetPluginStateQuery, GetPluginStateResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(query: GetPluginStateQuery): Promise<GetPluginStateResult> {
    const idResult = PluginId.create(query.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const state = await this.runtimeManager.getPluginState(idResult.value);
    return { pluginId: query.pluginId, state };
  }
}
