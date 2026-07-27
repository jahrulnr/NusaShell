import { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";

declare const pluginVersionBrand: unique symbol;

export type PluginVersion = string & { readonly [pluginVersionBrand]: true };

class PluginVersionValidationError extends DomainError {
  readonly code = "VALIDATION_ERROR" as const;

  constructor(message: string) {
    super(message);
  }
}

const SEMVER_PATTERN =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[\da-z-]+(?:\.[\da-z-]+)*)?(?:\+[\da-z-]+(?:\.[\da-z-]+)*)?$/i;

export const PluginVersion = {
  create(raw: string): Result<PluginVersion, DomainError> {
    const trimmed = raw.trim();
    if (!SEMVER_PATTERN.test(trimmed)) {
      return err(
        new PluginVersionValidationError(
          `Plugin version must be valid semver: ${raw}`,
        ),
      );
    }
    return ok(trimmed as PluginVersion);
  },

  toString(version: PluginVersion): string {
    return version;
  },

  equals(left: PluginVersion, right: PluginVersion): boolean {
    return left === right;
  },
};
