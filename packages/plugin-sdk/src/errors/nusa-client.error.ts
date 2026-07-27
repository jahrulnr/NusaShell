export class NusaClientError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "NusaClientError";
    this.code = code;
  }
}
