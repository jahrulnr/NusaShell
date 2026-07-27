import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { ListPluginsQuery } from "./list-plugins.query.js";
import type {
  ListPluginsResult,
  PluginListItem,
} from "./list-plugins.result.js";

export class ListPluginsHandler
  implements QueryHandler<ListPluginsQuery, ListPluginsResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(_query: ListPluginsQuery): Promise<ListPluginsResult> {
    const views = await this.runtimeManager.listPlugins();
    const items: PluginListItem[] = views.map((view) => ({
      pluginId: view.pluginId,
      name: view.pluginId,
      version: "0.0.0",
      state: view.state,
      enabled: true,
    }));
    return { plugins: items };
  }
}
