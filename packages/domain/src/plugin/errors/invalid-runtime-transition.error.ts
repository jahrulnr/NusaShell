import { DomainError } from "../../shared/domain-error.js";

export class InvalidRuntimeTransitionError extends DomainError {
  readonly code = "INVALID_RUNTIME_TRANSITION" as const;

  constructor(from: string, to: string) {
    super(`Invalid runtime transition from ${from} to ${to}`, { from, to });
  }
}
