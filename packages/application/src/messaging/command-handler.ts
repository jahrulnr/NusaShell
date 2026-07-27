import type { Command, CommandResult } from "./command.js";

export interface CommandHandler<TCommand extends Command, TResult = unknown> {
  handle(command: TCommand): CommandResult<TResult>;
}
