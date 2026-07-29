import { describe, expect, it } from "vitest";
import {
  RequestSchema,
  PluginStartRequestSchema,
  PluginStopRequestSchema,
  PluginListRequestSchema,
  ToolCallRequestSchema,
  ToolCancelRequestSchema,
  AgentRunRequestSchema,
  AgentCancelRequestSchema,
} from "../src/index.js";

describe("Request schemas", () => {
  describe("plugin.start", () => {
    it("parses a valid request", () => {
      const result = PluginStartRequestSchema.safeParse({
        kind: "request",
        id: "req_001",
        method: "plugin.start",
        payload: { pluginId: "com.example.notes" },
      });
      expect(result.success).toBe(true);
    });

    it("rejects missing pluginId", () => {
      const result = PluginStartRequestSchema.safeParse({
        kind: "request",
        id: "req_001",
        method: "plugin.start",
        payload: {},
      });
      expect(result.success).toBe(false);
    });
  });

  describe("plugin.stop", () => {
    it("parses a valid request", () => {
      const result = PluginStopRequestSchema.safeParse({
        kind: "request",
        id: "req_002",
        method: "plugin.stop",
        payload: { pluginId: "com.example.notes" },
      });
      expect(result.success).toBe(true);
    });
  });

  describe("plugin.list", () => {
    it("parses with empty payload", () => {
      const result = PluginListRequestSchema.safeParse({
        kind: "request",
        id: "req_003",
        method: "plugin.list",
        payload: {},
      });
      expect(result.success).toBe(true);
    });

    it("parses with missing payload", () => {
      const result = PluginListRequestSchema.safeParse({
        kind: "request",
        id: "req_003",
        method: "plugin.list",
      });
      expect(result.success).toBe(true);
    });
  });

  describe("tool.call", () => {
    it("parses a valid request", () => {
      const result = ToolCallRequestSchema.safeParse({
        kind: "request",
        id: "req_004",
        method: "tool.call",
        payload: {
          pluginId: "com.example.notes",
          requestId: "req-uuid-001",
          toolName: "echo",
          args: { message: "hello" },
        },
      });
      expect(result.success).toBe(true);
    });

    it("parses with optional timeoutMs", () => {
      const result = ToolCallRequestSchema.safeParse({
        kind: "request",
        id: "req_004",
        method: "tool.call",
        payload: {
          pluginId: "com.example.notes",
          requestId: "req-uuid-001",
          toolName: "echo",
          args: {},
          timeoutMs: 5000,
        },
      });
      expect(result.success).toBe(true);
    });

    it("rejects missing toolName", () => {
      const result = ToolCallRequestSchema.safeParse({
        kind: "request",
        id: "req_004",
        method: "tool.call",
        payload: {
          pluginId: "com.example.notes",
          requestId: "req-uuid-001",
          args: {},
        },
      });
      expect(result.success).toBe(false);
    });
  });

  describe("tool.cancel", () => {
    it("parses a valid request", () => {
      const result = ToolCancelRequestSchema.safeParse({
        kind: "request",
        id: "req_005",
        method: "tool.cancel",
        payload: {
          pluginId: "com.example.notes",
          requestId: "req-uuid-001",
        },
      });
      expect(result.success).toBe(true);
    });
  });

  describe("agent.cancel", () => {
    it("requires the active turn trace ID", () => {
      expect(AgentCancelRequestSchema.safeParse({
        kind: "request",
        id: "req_agent_cancel",
        method: "agent.cancel",
        payload: { traceId: "trace-1" },
      }).success).toBe(true);
      expect(AgentCancelRequestSchema.safeParse({
        kind: "request",
        id: "req_agent_cancel",
        method: "agent.cancel",
        payload: {},
      }).success).toBe(false);
    });
  });

  describe("agent.run", () => {
    it("preserves an explicit vision capability for the selected model", () => {
      const result = AgentRunRequestSchema.safeParse({
        kind: "request",
        id: "req_agent_vision",
        method: "agent.run",
        payload: {
          pluginIds: [],
          messages: [{ role: "user", content: "Describe the image" }],
          modelCapabilities: { supportsVision: false },
        },
      });

      expect(result.success).toBe(true);
      if (result.success) expect(result.data.payload.modelCapabilities?.supportsVision).toBe(false);
    });
  });

  describe("discriminated union", () => {
    it("routes to correct schema by method", () => {
      const result = RequestSchema.safeParse({
        kind: "request",
        id: "req_006",
        method: "plugin.start",
        payload: { pluginId: "com.example.notes" },
      });
      expect(result.success).toBe(true);
      if (result.success) {
        expect(result.data.method).toBe("plugin.start");
      }
    });

    it("rejects unknown method", () => {
      const result = RequestSchema.safeParse({
        kind: "request",
        id: "req_007",
        method: "plugin.unknown",
        payload: {},
      });
      expect(result.success).toBe(false);
    });
  });
});
