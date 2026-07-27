import type { EventHandler, ApplicationEvent } from "@nusashell/application";
import type { EventEnvelope } from "@nusashell/contracts";
import { mapDomainEvent } from "../mapping/client-event.mapper.js";
import type { SessionRegistry } from "../server/session-registry.js";

export class WebSocketEventPublisher implements EventHandler {
  constructor(private readonly registry: SessionRegistry) {}

  async handle(event: ApplicationEvent): Promise<void> {
    const envelope = mapDomainEvent(event);
    if (!envelope) return;

    this.broadcast(envelope);
  }

  private broadcast(envelope: EventEnvelope): void {
    for (const session of this.registry.all) {
      if (session.isOpen) {
        session.sendEvent(envelope);
      }
    }
  }
}
