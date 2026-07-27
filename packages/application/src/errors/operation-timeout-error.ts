import { ApplicationError } from "./application-error.js";

export class OperationTimeoutError extends ApplicationError {
  constructor(
    message: string,
    details?: Readonly<Record<string, unknown>>,
  ) {
    super("OPERATION_TIMEOUT", message, details);
    this.name = "OperationTimeoutError";
  }
}
