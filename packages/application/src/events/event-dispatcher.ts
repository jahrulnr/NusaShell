import type { DomainEvent } from "@nusashell/domain";

export type ApplicationEvent = DomainEvent;

export interface EventHandler<TEvent extends ApplicationEvent = ApplicationEvent> {
  handle(event: TEvent): Promise<void> | void;
}

export type EventHandlerFn<TEvent extends ApplicationEvent = ApplicationEvent> = (
  event: TEvent,
) => Promise<void> | void;

export class EventDispatcher {
  private readonly handlers = new Map<string, EventHandler[]>();
  private readonly globalHandlers: EventHandler[] = [];

  on<TEvent extends ApplicationEvent>(
    eventType: string,
    handler: EventHandler<TEvent>,
  ): void {
    const list = this.handlers.get(eventType);
    if (list) {
      list.push(handler as EventHandler);
    } else {
      this.handlers.set(eventType, [handler as EventHandler]);
    }
  }

  onAny(handler: EventHandler): void {
    this.globalHandlers.push(handler);
  }

  async publish(event: ApplicationEvent): Promise<void> {
    const typed = this.handlers.get(event.type) ?? [];
    const all = [...this.globalHandlers, ...typed];
    for (const handler of all) {
      await handler.handle(event);
    }
  }

  async publishAll(events: readonly ApplicationEvent[]): Promise<void> {
    for (const event of events) {
      await this.publish(event);
    }
  }
}
