import type {
  SuccessResponseEnvelope,
  ErrorResponseEnvelope,
  ResponseEnvelope,
} from "@nusashell/contracts";
import type { ProtocolError } from "../protocol/websocket-error.js";

export function mapSuccessResponse(
  requestId: string,
  result: unknown,
): SuccessResponseEnvelope {
  return {
    kind: "response",
    id: requestId,
    ok: true,
    result,
  };
}

export function mapErrorResponse(
  requestId: string,
  error: ProtocolError,
): ErrorResponseEnvelope {
  return {
    kind: "response",
    id: requestId,
    ok: false,
    error: {
      code: error.code,
      message: error.message,
      ...(error.details !== undefined ? { details: error.details } : {}),
    },
  };
}

export function mapResponse(
  requestId: string,
  result: unknown,
  error: ProtocolError | null,
): ResponseEnvelope {
  if (error) {
    return mapErrorResponse(requestId, error);
  }
  return mapSuccessResponse(requestId, result);
}
