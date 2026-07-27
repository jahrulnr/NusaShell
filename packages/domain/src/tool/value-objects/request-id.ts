import { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";

declare const requestIdBrand: unique symbol;

export type RequestId = string & { readonly [requestIdBrand]: true };

class RequestIdValidationError extends DomainError {
  readonly code = "VALIDATION_ERROR" as const;

  constructor(message: string) {
    super(message);
  }
}

const REQUEST_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export const RequestId = {
  create(raw: string): Result<RequestId, DomainError> {
    const trimmed = raw.trim();
    if (!REQUEST_ID_PATTERN.test(trimmed)) {
      return err(
        new RequestIdValidationError("Request id must be a valid UUID"),
      );
    }
    return ok(trimmed as RequestId);
  },

  toString(id: RequestId): string {
    return id;
  },

  equals(left: RequestId, right: RequestId): boolean {
    return left === right;
  },
};
