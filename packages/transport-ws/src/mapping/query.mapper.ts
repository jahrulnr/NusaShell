import type { ParsedRequest } from "@nusashell/contracts";
import type { GetPromptQuery, GetPluginQuery, GetPluginStateQuery, ListPluginsQuery, ListPromptsQuery, ListResourcesQuery, ListResourceTemplatesQuery, ListToolsQuery, ReadResourceQuery, SystemPingQuery, SystemVersionQuery } from "@nusashell/application";

export function mapToQuery(
  request: ParsedRequest,
): ListPluginsQuery | GetPluginQuery | GetPluginStateQuery | ListToolsQuery | ListPromptsQuery | GetPromptQuery | ListResourcesQuery | ListResourceTemplatesQuery | ReadResourceQuery | SystemPingQuery | SystemVersionQuery | null {
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
    case "prompt.list":
      return { kind: "list-prompts", pluginId: request.payload.pluginId } as ListPromptsQuery;
    case "prompt.get":
      return { kind: "get-prompt", pluginId: request.payload.pluginId, name: request.payload.name, args: request.payload.args } as GetPromptQuery;
    case "resource.list":
      return { kind: "list-resources", pluginId: request.payload.pluginId } as ListResourcesQuery;
    case "resource.template.list":
      return { kind: "list-resource-templates", pluginId: request.payload.pluginId } as ListResourceTemplatesQuery;
    case "resource.read":
      return { kind: "read-resource", pluginId: request.payload.pluginId, uri: request.payload.uri } as ReadResourceQuery;
    case "system.ping":
      return { kind: "system-ping" } as SystemPingQuery;
    case "system.version":
      return { kind: "system-version" } as SystemVersionQuery;
    default:
      return null;
  }
}
