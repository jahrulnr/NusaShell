import { describe, expect, it } from "vitest";
import {
  EventSchema,
  PluginStartedEventSchema,
  PluginStoppedEventSchema,
  PluginCrashedEventSchema,
  PluginStateChangedEventSchema,
  ToolCallCompletedEventSchema,
} from "../src/index.js";

describe("Event schemas", () => {
  it("parses plugin.started event", () => {
    const result = PluginStartedEventSchema.safeParse({
      kind: "event",
      event: "plugin.started",
      payload: {
        pluginId: "com.example.notes",
        state: "running",
        pid: 12345,
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.stopped event", () => {
    const result = PluginStoppedEventSchema.safeParse({
      kind: "event",
      event: "plugin.stopped",
      payload: {
        pluginId: "com.example.notes",
        state: "idle",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.crashed event", () => {
    const result = PluginCrashedEventSchema.safeParse({
      kind: "event",
      event: "plugin.crashed",
      payload: {
        pluginId: "com.example.notes",
        state: "crashed",
        exitCode: 1,
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.state_changed event", () => {
    const result = PluginStateChangedEventSchema.safeParse({
      kind: "event",
      event: "plugin.state_changed",
      payload: {
        pluginId: "com.example.notes",
        oldState: "starting",
        newState: "running",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses tool.call_completed event", () => {
    const result = ToolCallCompletedEventSchema.safeParse({
      kind: "event",
      event: "tool.call_completed",
      payload: {
        pluginId: "com.example.notes",
        requestId: "req-uuid-001",
        toolName: "echo",
        success: true,
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("discriminates by event field", () => {
    const result = EventSchema.safeParse({
      kind: "event",
      event: "plugin.started",
      payload: {
        pluginId: "com.example.notes",
        state: "running",
        pid: 12345,
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.event).toBe("plugin.started");
    }
  });

  it("rejects unknown event type", () => {
    const result = EventSchema.safeParse({
      kind: "event",
      event: "plugin.unknown",
      payload: {},
    });
    expect(result.success).toBe(false);
  });
});
