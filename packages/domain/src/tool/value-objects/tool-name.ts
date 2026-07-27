import { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";

declare const toolNameBrand: unique symbol;

export type ToolName = string & { readonly [toolNameBrand]: true };

class ToolNameValidationError extends DomainError {
  readonly code = "VALIDATION_ERROR" as const;

  constructor(message: string) {
    super(message);
  }
}

const TOOL_NAME_PATTERN = /^[a-zA-Z][a-zA-Z0-9_-]*$/;

export const ToolName = {
  create(raw: string): Result<ToolName, DomainError> {
    const trimmed = raw.trim();
    if (trimmed.length === 0) {
      return err(new ToolNameValidationError("Tool name must not be empty"));
    }
    if (!TOOL_NAME_PATTERN.test(trimmed)) {
      return err(
        new ToolNameValidationError(
          "Tool name must start with a letter and contain only letters, numbers, underscores, or dashes",
        ),
      );
    }
    return ok(trimmed as ToolName);
  },

  toString(name: ToolName): string {
    return name;
  },

  equals(left: ToolName, right: ToolName): boolean {
    return left === right;
  },
};
