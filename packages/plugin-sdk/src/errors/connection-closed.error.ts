import { NusaClientError } from "./nusa-client.error.js";

export class ConnectionClosedError extends NusaClientError {
  constructor(message = "WebSocket connection closed") {
    super("CONNECTION_CLOSED", message);
    this.name = "ConnectionClosedError";
  }
}
