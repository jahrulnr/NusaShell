import { ApplicationError, type ApplicationErrorCode } from "@nusashell/application";
import { ProtocolError } from "../protocol/websocket-error.js";

const ERROR_CODE_MAP: Record<ApplicationErrorCode, string> = {
  PLUGIN_NOT_FOUND: "PLUGIN_NOT_FOUND",
  PLUGIN_DISABLED: "PLUGIN_DISABLED",
  PLUGIN_NOT_RUNNING: "PLUGIN_NOT_RUNNING",
  INVALID_RUNTIME_TRANSITION: "INVALID_RUNTIME_TRANSITION",
  PLUGIN_START_FAILED: "PLUGIN_START_FAILED",
  PLUGIN_STOP_FAILED: "PLUGIN_STOP_FAILED",
  PLUGIN_CRASHED: "PLUGIN_CRASHED",
  TOOL_NOT_FOUND: "TOOL_NOT_FOUND",
  TOOL_CALL_TIMEOUT: "TOOL_CALL_TIMEOUT",
  TOOL_CALL_CANCELLED: "TOOL_CALL_CANCELLED",
  MCP_CONNECTION_FAILED: "MCP_CONNECTION_FAILED",
  OPERATION_CONFLICT: "OPERATION_CONFLICT",
  OPERATION_TIMEOUT: "OPERATION_TIMEOUT",
  INTERNAL_ERROR: "INTERNAL_ERROR",
};

export function mapApplicationError(
  error: ApplicationError,
  requestId?: string,
): ProtocolError {
  const code = ERROR_CODE_MAP[error.code] ?? "INTERNAL_ERROR";
  return new ProtocolError(code, error.message, requestId, error.details);
}
