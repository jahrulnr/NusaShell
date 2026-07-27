import { NusaClientError } from "./nusa-client.error.js";

export class RequestTimeoutError extends NusaClientError {
  readonly requestId: string;

  constructor(requestId: string, timeoutMs: number) {
    super("REQUEST_TIMEOUT", `Request ${requestId} timed out after ${timeoutMs}ms`);
    this.name = "RequestTimeoutError";
    this.requestId = requestId;
  }
}
