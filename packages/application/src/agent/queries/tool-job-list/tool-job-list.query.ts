import type { Query } from "../../../messaging/query.js";

export interface ToolJobListQuery extends Query {
  readonly kind: "tool-job-list";
  readonly conversationId: string;
}
