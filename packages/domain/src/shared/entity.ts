import type { DomainEvent } from "./domain-event.js";

export abstract class Entity<TId> {
  protected constructor(readonly id: TId) {}

  private readonly uncommittedEvents: DomainEvent[] = [];

  protected record(event: DomainEvent): void {
    this.uncommittedEvents.push(event);
  }

  pullEvents(): readonly DomainEvent[] {
    const events = [...this.uncommittedEvents];
    this.uncommittedEvents.length = 0;
    return events;
  }
}
