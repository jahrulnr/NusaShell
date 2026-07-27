export interface WebSocketError {
  readonly code: string;
  readonly message: string;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;
}

export class ProtocolError extends Error {
  readonly code: string;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;

  constructor(
    code: string,
    message: string,
    requestId?: string,
    details?: Readonly<Record<string, unknown>>,
  ) {
    super(message);
    this.name = "ProtocolError";
    this.code = code;
    if (requestId !== undefined) {
      this.requestId = requestId;
    }
    if (details !== undefined) {
      this.details = details;
    }
  }
}
