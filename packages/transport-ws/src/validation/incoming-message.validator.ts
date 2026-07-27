import { RequestSchema, type ParsedRequest } from "@nusashell/contracts";
import { ProtocolError } from "../protocol/websocket-error.js";

export interface ValidationResult {
  readonly success: true;
  readonly request: ParsedRequest;
}

export interface ValidationError {
  readonly success: false;
  readonly error: ProtocolError;
}

export function validateIncomingMessage(raw: unknown): ValidationResult | ValidationError {
  let parsed: unknown;
  try {
    parsed = typeof raw === "string" ? JSON.parse(raw) : raw;
  } catch {
    return {
      success: false,
      error: new ProtocolError("INVALID_REQUEST", "Malformed JSON"),
    };
  }

  const result = RequestSchema.safeParse(parsed);
  if (!result.success) {
    const firstIssue = result.error.issues[0];
    const message = firstIssue
      ? `${firstIssue.path.join(".")}: ${firstIssue.message}`
      : "Validation failed";

    const requestId =
      parsed && typeof parsed === "object" && "id" in parsed && typeof (parsed as Record<string, unknown>).id === "string"
        ? (parsed as Record<string, unknown>).id as string
        : undefined;

    return {
      success: false,
      error: new ProtocolError("INVALID_REQUEST", message, requestId),
    };
  }

  return { success: true, request: result.data };
}
