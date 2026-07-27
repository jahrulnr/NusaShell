import type { Query } from "../../../messaging/query.js";

export interface SystemVersionQuery extends Query {
  readonly kind: "system-version";
}
