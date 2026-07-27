export type ApplicationErrorCode =
  | "PLUGIN_NOT_FOUND"
  | "PLUGIN_DISABLED"
  | "INVALID_RUNTIME_TRANSITION"
  | "PLUGIN_START_FAILED"
  | "PLUGIN_STOP_FAILED"
  | "PLUGIN_CRASHED"
  | "TOOL_NOT_FOUND"
  | "TOOL_CALL_TIMEOUT"
  | "TOOL_CALL_CANCELLED"
  | "MCP_CONNECTION_FAILED"
  | "OPERATION_CONFLICT"
  | "OPERATION_TIMEOUT"
  | "INTERNAL_ERROR";

export class ApplicationError extends Error {
  readonly code: ApplicationErrorCode;
  readonly details?: Readonly<Record<string, unknown>>;

  constructor(
    code: ApplicationErrorCode,
    message: string,
    details?: Readonly<Record<string, unknown>>,
  ) {
    super(message);
    this.name = "ApplicationError";
    this.code = code;
    if (details !== undefined) {
      this.details = details;
    }
  }
}
