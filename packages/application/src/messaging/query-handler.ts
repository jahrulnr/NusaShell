import type { Query, QueryResult } from "./query.js";

export interface QueryHandler<TQuery extends Query, TResult = unknown> {
  handle(query: TQuery): QueryResult<TResult>;
}
