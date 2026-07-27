import { DomainError } from "../../shared/domain-error.js";

export class PluginDisabledError extends DomainError {
  readonly code = "PLUGIN_DISABLED" as const;

  constructor(pluginId: string) {
    super(`Plugin ${pluginId} is disabled`, { pluginId });
  }
}
