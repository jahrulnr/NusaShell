import { DomainError } from "../../shared/domain-error.js";

export class PluginNotFoundError extends DomainError {
  readonly code = "PLUGIN_NOT_FOUND" as const;

  constructor(pluginId: string) {
    super(`Plugin ${pluginId} was not found`, { pluginId });
  }
}
