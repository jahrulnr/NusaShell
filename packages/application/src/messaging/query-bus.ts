import type { Query, QueryResult } from "./query.js";
import type { QueryHandler } from "./query-handler.js";

type AnyQueryHandler = QueryHandler<Query, unknown>;

export class QueryBus {
  private readonly handlers = new Map<string, AnyQueryHandler>();

  register<TQuery extends Query, TResult>(
    kind: string,
    handler: QueryHandler<TQuery, TResult>,
  ): void {
    if (this.handlers.has(kind)) {
      throw new Error(`Query kind "${kind}" is already registered`);
    }
    this.handlers.set(kind, handler as AnyQueryHandler);
  }

  async execute<TQuery extends Query, TResult = unknown>(
    query: TQuery,
  ): QueryResult<TResult> {
    const handler = this.handlers.get(query.kind);
    if (!handler) {
      throw new Error(`No handler registered for query "${query.kind}"`);
    }
    return handler.handle(query) as QueryResult<TResult>;
  }
}
