import type { EventHandler, ApplicationEvent } from "@nusashell/application";
import type { EventEnvelope } from "@nusashell/contracts";
import { mapDomainEvent } from "../mapping/client-event.mapper.js";
import type { SessionRegistry } from "../server/session-registry.js";
import type { ClientSubscriptionRegistry } from "./client-subscription-registry.js";

export class WebSocketEventPublisher implements EventHandler {
  private sequenceCounter = 0;

  constructor(
    private readonly registry: SessionRegistry,
    private readonly subscriptions?: ClientSubscriptionRegistry,
  ) {}

  async handle(event: ApplicationEvent): Promise<void> {
    const sequence = ++this.sequenceCounter;
    const envelope = mapDomainEvent(event, sequence);
    if (!envelope) return;

    this.broadcast(envelope);
  }

  private broadcast(envelope: EventEnvelope): void {
    for (const session of this.registry.all) {
      if (!session.isOpen) continue;
      if (this.subscriptions && !this.subscriptions.isSubscribed(session.id, envelope.event)) {
        continue;
      }
      session.sendEvent(envelope);
    }
  }
}
