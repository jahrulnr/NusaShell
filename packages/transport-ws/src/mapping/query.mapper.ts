import type { ParsedRequest } from "@nusashell/contracts";
import type { ListPluginsQuery, GetPluginQuery, GetPluginStateQuery, ListToolsQuery, SystemPingQuery, SystemVersionQuery } from "@nusashell/application";

export function mapToQuery(
  request: ParsedRequest,
): ListPluginsQuery | GetPluginQuery | GetPluginStateQuery | ListToolsQuery | SystemPingQuery | SystemVersionQuery | null {
  switch (request.method) {
    case "plugin.list":
      return { kind: "list-plugins" } as ListPluginsQuery;
    case "plugin.get":
      return {
        kind: "get-plugin",
        pluginId: request.payload.pluginId,
      } as GetPluginQuery;
    case "plugin.state":
      return {
        kind: "get-plugin-state",
        pluginId: request.payload.pluginId,
      } as GetPluginStateQuery;
    case "tool.list":
      return {
        kind: "list-tools",
        pluginId: request.payload.pluginId,
      } as ListToolsQuery;
    case "system.ping":
      return { kind: "system-ping" } as SystemPingQuery;
    case "system.version":
      return { kind: "system-version" } as SystemVersionQuery;
    default:
      return null;
  }
}
