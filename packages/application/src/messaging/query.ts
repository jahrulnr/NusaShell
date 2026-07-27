export interface Query {
  readonly kind: string;
}

export type QueryResult<T = unknown> = Promise<T>;
