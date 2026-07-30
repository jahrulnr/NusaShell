import { describe, expect, it } from "vitest";
import {
  EventSchema,
  PluginInstalledEventSchema,
  PluginUninstalledEventSchema,
  PluginStartedEventSchema,
  PluginStoppedEventSchema,
  PluginCrashedEventSchema,
  PluginStateChangedEventSchema,
  ToolCallCompletedEventSchema,
} from "../src/index.js";

describe("Event schemas", () => {
  it("parses plugin.installed event", () => {
    const result = PluginInstalledEventSchema.safeParse({
      kind: "event",
      event: "plugin.installed",
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
        version: "1.0.0",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.uninstalled event", () => {
    const result = PluginUninstalledEventSchema.safeParse({
      kind: "event",
      event: "plugin.uninstalled",
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.installed via EventSchema discriminated union", () => {
    const result = EventSchema.safeParse({
      kind: "event",
      event: "plugin.installed",
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
        version: "1.0.0",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.event).toBe("plugin.installed");
    }
  });

  it("parses plugin.uninstalled via EventSchema discriminated union", () => {
    const result = EventSchema.safeParse({
      kind: "event",
      event: "plugin.uninstalled",
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.event).toBe("plugin.uninstalled");
    }
  });

  it("parses plugin.started event", () => {
    const result = PluginStartedEventSchema.safeParse({
      kind: "event",
      event: "plugin.started",
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
      sequence: 1,
      payload: {
        pluginId: "nusashell.notes",
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
