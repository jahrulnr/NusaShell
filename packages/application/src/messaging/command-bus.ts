import type { Command, CommandResult } from "./command.js";
import type { CommandHandler } from "./command-handler.js";

type AnyCommandHandler = CommandHandler<Command, unknown>;

export class CommandBus {
  private readonly handlers = new Map<string, AnyCommandHandler>();

  register<TCommand extends Command, TResult>(
    kind: string,
    handler: CommandHandler<TCommand, TResult>,
  ): void {
    if (this.handlers.has(kind)) {
      throw new Error(`Command kind "${kind}" is already registered`);
    }
    this.handlers.set(kind, handler as AnyCommandHandler);
  }

  async execute<TCommand extends Command, TResult = unknown>(
    command: TCommand,
  ): CommandResult<TResult> {
    const handler = this.handlers.get(command.kind);
    if (!handler) {
      throw new Error(`No handler registered for command "${command.kind}"`);
    }
    return handler.handle(command) as CommandResult<TResult>;
  }
}
