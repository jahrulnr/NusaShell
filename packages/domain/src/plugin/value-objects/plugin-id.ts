import { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";

declare const pluginIdBrand: unique symbol;

export type PluginId = string & { readonly [pluginIdBrand]: true };

class PluginIdValidationError extends DomainError {
  readonly code = "VALIDATION_ERROR" as const;

  constructor(message: string) {
    super(message);
  }
}

const PLUGIN_ID_PATTERN = /^[a-z0-9][a-z0-9._-]*$/i;

export const PluginId = {
  create(raw: string): Result<PluginId, DomainError> {
    const trimmed = raw.trim();
    if (trimmed.length === 0) {
      return err(new PluginIdValidationError("Plugin id must not be empty"));
    }
    if (!PLUGIN_ID_PATTERN.test(trimmed)) {
      return err(
        new PluginIdValidationError(
          "Plugin id must start with alphanumeric and contain only letters, numbers, dots, dashes, or underscores",
        ),
      );
    }
    return ok(trimmed as PluginId);
  },

  toString(id: PluginId): string {
    return id;
  },

  equals(left: PluginId, right: PluginId): boolean {
    return left === right;
  },
};
