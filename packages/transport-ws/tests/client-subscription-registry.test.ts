import { describe, expect, it } from "vitest";
import { ClientSubscriptionRegistry } from "../src/events/client-subscription-registry.js";

describe("ClientSubscriptionRegistry", () => {
  it("returns false when session has no subscriptions", () => {
    const registry = new ClientSubscriptionRegistry();
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(false);
  });

  it("returns true after subscribing to specific event type", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["plugin.started"]);
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(true);
    expect(registry.isSubscribed("sess-1", "plugin.stopped")).toBe(false);
  });

  it("returns true for all events when subscribed with wildcard", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["*"]);
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(true);
    expect(registry.isSubscribed("sess-1", "plugin.stopped")).toBe(true);
    expect(registry.isSubscribed("sess-1", "tool.call_completed")).toBe(true);
  });

  it("returns true for all events when eventTypes omitted (defaults to wildcard)", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["*"]);
    expect(registry.isSubscribed("sess-1", "plugin.crashed")).toBe(true);
  });

  it("removes specific event type on unsubscribe", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["plugin.started", "plugin.stopped"]);
    registry.unsubscribe("sess-1", ["plugin.started"]);
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(false);
    expect(registry.isSubscribed("sess-1", "plugin.stopped")).toBe(true);
  });

  it("removes all subscriptions on unsubscribe without eventTypes", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["plugin.started", "plugin.stopped"]);
    registry.unsubscribe("sess-1");
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(false);
    expect(registry.isSubscribed("sess-1", "plugin.stopped")).toBe(false);
  });

  it("clears all subscriptions for a session", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["*"]);
    registry.clear("sess-1");
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(false);
  });

  it("does not affect other sessions", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["plugin.started"]);
    registry.subscribe("sess-2", ["plugin.stopped"]);
    registry.clear("sess-1");
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(false);
    expect(registry.isSubscribed("sess-2", "plugin.stopped")).toBe(true);
  });

  it("supports multiple event types in one subscribe call", () => {
    const registry = new ClientSubscriptionRegistry();
    registry.subscribe("sess-1", ["plugin.started", "plugin.crashed", "tool.call_completed"]);
    expect(registry.isSubscribed("sess-1", "plugin.started")).toBe(true);
    expect(registry.isSubscribed("sess-1", "plugin.crashed")).toBe(true);
    expect(registry.isSubscribed("sess-1", "tool.call_completed")).toBe(true);
    expect(registry.isSubscribed("sess-1", "plugin.stopped")).toBe(false);
  });
});
