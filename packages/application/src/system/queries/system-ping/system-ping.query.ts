import type { Query } from "../../../messaging/query.js";

export interface SystemPingQuery extends Query {
  readonly kind: "system-ping";
}
