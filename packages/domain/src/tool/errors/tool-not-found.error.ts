import { DomainError } from "../../shared/domain-error.js";

export class ToolNotFoundError extends DomainError {
  readonly code = "TOOL_NOT_FOUND" as const;

  constructor(toolName: string, pluginId: string) {
    super(`Tool ${toolName} was not found for plugin ${pluginId}`, {
      toolName,
      pluginId,
    });
  }
}
