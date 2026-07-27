import { PluginId } from "@nusashell/domain";
import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { ListToolsQuery } from "./list-tools.query.js";
import type { ListToolsResult, ToolItem } from "./list-tools.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class ListToolsHandler
  implements QueryHandler<ListToolsQuery, ListToolsResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(query: ListToolsQuery): Promise<ListToolsResult> {
    const idResult = PluginId.create(query.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const tools = await this.runtimeManager.listTools(idResult.value);
    const items: ToolItem[] = tools.map((t) => ({
      name: t.name,
      ...(t.description !== undefined ? { description: t.description } : {}),
      ...(t.inputSchema !== undefined ? { inputSchema: t.inputSchema } : {}),
    }));
    return { tools: items };
  }
}
