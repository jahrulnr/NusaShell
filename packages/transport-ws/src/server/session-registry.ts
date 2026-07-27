import { WebSocketSession } from "./websocket-session.js";

export class SessionRegistry {
  private readonly sessions = new Map<string, WebSocketSession>();

  add(session: WebSocketSession): void {
    this.sessions.set(session.id, session);
  }

  remove(sessionId: string): void {
    this.sessions.delete(sessionId);
  }

  get(sessionId: string): WebSocketSession | undefined {
    return this.sessions.get(sessionId);
  }

  get all(): readonly WebSocketSession[] {
    return [...this.sessions.values()];
  }

  clear(): void {
    for (const session of this.sessions.values()) {
      session.close();
    }
    this.sessions.clear();
  }
}
