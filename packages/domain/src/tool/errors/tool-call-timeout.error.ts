import { DomainError } from "../../shared/domain-error.js";

export class ToolCallTimeoutError extends DomainError {
  readonly code = "TOOL_CALL_TIMEOUT" as const;

  constructor(requestId: string, pluginId: string) {
    super(`Tool call ${requestId} timed out for plugin ${pluginId}`, {
      requestId,
      pluginId,
    });
  }
}
