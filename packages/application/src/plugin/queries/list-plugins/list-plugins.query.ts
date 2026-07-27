import type { Query } from "../../../messaging/query.js";

export interface ListPluginsQuery extends Query {
  readonly kind: "list-plugins";
}
