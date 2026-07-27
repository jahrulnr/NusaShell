import type { EventType } from "@nusashell/contracts";

const ALL_EVENTS = "*";

export class ClientSubscriptionRegistry {
  private readonly subscriptions = new Map<string, Set<string>>();

  subscribe(sessionId: string, eventTypes: string[]): void {
    let set = this.subscriptions.get(sessionId);
    if (!set) {
      set = new Set();
      this.subscriptions.set(sessionId, set);
    }
    for (const type of eventTypes) {
      set.add(type);
    }
  }

  unsubscribe(sessionId: string, eventTypes?: string[]): void {
    const set = this.subscriptions.get(sessionId);
    if (!set) return;

    if (!eventTypes || eventTypes.length === 0) {
      this.subscriptions.delete(sessionId);
      return;
    }

    for (const type of eventTypes) {
      set.delete(type);
    }
    if (set.size === 0) {
      this.subscriptions.delete(sessionId);
    }
  }

  isSubscribed(sessionId: string, eventType: EventType): boolean {
    const set = this.subscriptions.get(sessionId);
    if (!set) return false;
    return set.has(ALL_EVENTS) || set.has(eventType);
  }

  clear(sessionId: string): void {
    this.subscriptions.delete(sessionId);
  }
}
