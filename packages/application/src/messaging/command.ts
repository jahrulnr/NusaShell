export interface Command {
  readonly kind: string;
}

export type CommandResult<T = unknown> = Promise<T>;
