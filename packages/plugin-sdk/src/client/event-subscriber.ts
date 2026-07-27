import type { EventType, EventEnvelope } from "@nusashell/contracts";

type EventHandler<TPayload = unknown> = (payload: TPayload) => void;

export class EventSubscriber {
  private readonly handlers = new Map<EventType, Set<EventHandler>>();
  private readonly globalHandlers = new Set<EventHandler>();

  on<TPayload>(
    eventType: EventType,
    handler: EventHandler<TPayload>,
  ): () => void {
    const set = this.handlers.get(eventType) ?? new Set();
    set.add(handler as EventHandler);
    this.handlers.set(eventType, set);

    return () => {
      set.delete(handler as EventHandler);
      if (set.size === 0) {
        this.handlers.delete(eventType);
      }
    };
  }

  onAny(handler: EventHandler): () => void {
    this.globalHandlers.add(handler);
    return () => {
      this.globalHandlers.delete(handler);
    };
  }

  dispatch(envelope: EventEnvelope): void {
    for (const handler of this.globalHandlers) {
      handler(envelope.payload);
    }

    const set = this.handlers.get(envelope.event);
    if (set) {
      for (const handler of set) {
        handler(envelope.payload);
      }
    }
  }

  clear(): void {
    this.handlers.clear();
    this.globalHandlers.clear();
  }
}
