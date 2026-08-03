export type ApplicationErrorCode =
  | "PLUGIN_NOT_FOUND"
  | "PLUGIN_DISABLED"
  | "PLUGIN_NOT_RUNNING"
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
  | "AGENT_PROVIDER_NOT_FOUND"
  | "AGENT_INVALID_INPUT"
  | "AGENT_TOOL_NOT_ALLOWED"
  | "AGENT_MAX_TOOL_ROUNDS"
  | "AGENT_PROVIDER_FAILED"
  | "AGENT_TURN_CANCELLED"
  | "UNAUTHORIZED"
  | "UNAVAILABLE"
  | "INTERNAL_ERROR"
  | "JOB_NOT_FOUND"
  | "JOB_INVALID_SCHEDULE"
  | "JOB_INVALID_TRIGGER"
  | "JOB_NOT_RUNNING"
  | "PIPELINE_NOT_FOUND"
  | "PIPELINE_INVALID"
  | "ACP_SESSION_NOT_FOUND"
  | "ACP_PROVIDER_FAILED";

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
