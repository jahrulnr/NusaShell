export type DomainErrorCode =
  | "PLUGIN_NOT_FOUND"
  | "PLUGIN_DISABLED"
  | "INVALID_RUNTIME_TRANSITION"
  | "TOOL_NOT_FOUND"
  | "TOOL_CALL_TIMEOUT"
  | "VALIDATION_ERROR";

export abstract class DomainError {
  abstract readonly code: DomainErrorCode;

  constructor(
    readonly message: string,
    readonly details?: Readonly<Record<string, unknown>>,
  ) {}
}
