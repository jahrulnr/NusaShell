import type { Query } from "../../../messaging/query.js";

export interface GetPluginQuery extends Query {
  readonly kind: "get-plugin";
  readonly pluginId: string;
}
