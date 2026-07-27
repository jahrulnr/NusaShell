import type { Query } from "../../../messaging/query.js";

export interface ListToolsQuery extends Query {
  readonly kind: "list-tools";
  readonly pluginId: string;
}
