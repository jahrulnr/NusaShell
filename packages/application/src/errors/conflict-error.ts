import { ApplicationError } from "./application-error.js";

export class ConflictError extends ApplicationError {
  constructor(
    message: string,
    details?: Readonly<Record<string, unknown>>,
  ) {
    super("OPERATION_CONFLICT", message, details);
    this.name = "ConflictError";
  }
}
