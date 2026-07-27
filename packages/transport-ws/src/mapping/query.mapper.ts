import type { ParsedRequest } from "@nusashell/contracts";
import type { ListPluginsQuery } from "@nusashell/application";

export function mapToQuery(request: ParsedRequest): ListPluginsQuery | null {
  switch (request.method) {
    case "plugin.list":
      return { kind: "list-plugins" } as ListPluginsQuery;
    default:
      return null;
  }
}
