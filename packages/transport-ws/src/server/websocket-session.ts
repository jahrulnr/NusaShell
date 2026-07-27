import type { WebSocket } from "ws";
import type { ResponseEnvelope, EventEnvelope } from "@nusashell/contracts";

export class WebSocketSession {
  private closed = false;

  constructor(
    readonly id: string,
    private readonly ws: WebSocket,
  ) {
    ws.on("close", () => {
      this.closed = true;
    });
  }

  get isOpen(): boolean {
    return !this.closed && this.ws.readyState === this.ws.OPEN;
  }

  sendResponse(response: ResponseEnvelope): void {
    this.send(JSON.stringify(response));
  }

  sendEvent(event: EventEnvelope): void {
    this.send(JSON.stringify(event));
  }

  private send(data: string): void {
    if (this.isOpen) {
      this.ws.send(data);
    }
  }

  close(): void {
    this.closed = true;
    this.ws.close();
  }
}
