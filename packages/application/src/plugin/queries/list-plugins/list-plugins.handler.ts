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
      ...(view.ui !== undefined ? { ui: view.ui } : {}),
      keepAliveOnClose: view.keepAliveOnClose,
      ...(view.automation !== undefined ? { automation: view.automation } : {}),
    }));
    return { plugins: items };
  }
}
