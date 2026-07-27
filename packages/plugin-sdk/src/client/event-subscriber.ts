import type { EventType, EventEnvelope } from "@nusashell/contracts";

type EventHandler<TPayload = unknown> = (payload: TPayload, sequence: number) => void;

export class EventSubscriber {
  private readonly handlers = new Map<EventType, Set<EventHandler>>();
  private readonly globalHandlers = new Set<EventHandler>();
  private _lastSequence = 0;

  get lastSequence(): number {
    return this._lastSequence;
  }

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
    this._lastSequence = envelope.sequence;

    for (const handler of this.globalHandlers) {
      handler(envelope.payload, envelope.sequence);
    }

    const set = this.handlers.get(envelope.event);
    if (set) {
      for (const handler of set) {
        handler(envelope.payload, envelope.sequence);
      }
    }
  }

  clear(): void {
    this.handlers.clear();
    this.globalHandlers.clear();
    this._lastSequence = 0;
  }
}
