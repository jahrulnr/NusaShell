import type { AsyncToolRuntime, AsyncToolPeekResult } from "../../services/async-tool-runtime.js";
import type { ToolJobListQuery } from "./tool-job-list.query.js";
import type { QueryResult } from "../../../messaging/query.js";

export class ToolJobListHandler {
  constructor(private readonly runtime: AsyncToolRuntime) {}

  async handle(query: ToolJobListQuery): QueryResult<readonly AsyncToolPeekResult[]> {
    return this.runtime.list(query.conversationId);
  }
}
