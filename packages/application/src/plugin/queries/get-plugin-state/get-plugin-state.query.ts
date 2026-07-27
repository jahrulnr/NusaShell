import type { Query } from "../../../messaging/query.js";

export interface GetPluginStateQuery extends Query {
  readonly kind: "get-plugin-state";
  readonly pluginId: string;
}
